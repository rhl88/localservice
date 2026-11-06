package llmrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cmspro-local-service/internal/config"
)

// testEnv 测试环境：决策/回调服务器 + 已注入的 config
type testEnv struct {
	dispatchSrv *httptest.Server
	mu          sync.Mutex

	// dispatchHandler 可替换的决策处理函数（按调用次序返回不同决策）
	dispatchHandlers []func(w http.ResponseWriter, r *http.Request) bool
	dispatchCalls    int

	callbacks chan map[string]interface{} // 日志回调载荷通道
}

// setup 初始化测试环境：决策接口地址写入 config.CmsproBaseURL
func setup(t *testing.T, handlers ...func(w http.ResponseWriter, r *http.Request) bool) *testEnv {
	t.Helper()
	env := &testEnv{
		dispatchHandlers: handlers,
		callbacks:        make(chan map[string]interface{}, 8),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/cline/internal/dispatch", env.handleDispatch)
	mux.HandleFunc("/api/cline/internal/log-callback", env.handleCallback)
	env.dispatchSrv = httptest.NewServer(mux)
	t.Cleanup(env.dispatchSrv.Close)

	// 将决策接口地址注入全局 config（Save 同时设置内存 current）
	cfg := config.Default()
	cfg.CmsproBaseURL = env.dispatchSrv.URL
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })
	return env
}

// handleDispatch 按次序执行注入的决策处理函数，全部耗尽后返回 500
func (e *testEnv) handleDispatch(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	i := e.dispatchCalls
	e.dispatchCalls++
	e.mu.Unlock()

	if i < len(e.dispatchHandlers) {
		if e.dispatchHandlers[i](w, r) {
			return
		}
	}
	writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
		"code": 500, "message": "决策处理函数未覆盖", "data": nil,
	})
}

// handleCallback 记录日志回调载荷
func (e *testEnv) handleCallback(w http.ResponseWriter, r *http.Request) {
	var payload map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	select {
	case e.callbacks <- payload:
	default:
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"code": 0, "message": "success", "data": nil})
}

// dispatchOK 构造标准成功决策响应
func dispatchOK(t *testing.T, upstreamURL, authHeader string, policy RetryPolicy, accountID int64) func(w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) bool {
		data := DispatchData{
			DispatchID:  fmt.Sprintf("dp-%d", time.Now().UnixNano()),
			UpstreamURL: upstreamURL,
			AuthHeader:  authHeader,
			AccountID:   accountID,
			RetryPolicy: policy,
		}
		writeJSON(w, http.StatusOK, dispatchResponse{Code: 0, Message: "success", Data: &data})
		return true
	}
}

// waitCallback 等待一条日志回调（最多 2 秒）
func (e *testEnv) waitCallback(t *testing.T) map[string]interface{} {
	t.Helper()
	select {
	case p := <-e.callbacks:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("等待日志回调超时")
		return nil
	}
}

// sseServer 启动一个模拟上游 SSE 服务器
func sseServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

// waitIntermediateFailure 消费并断言一条中间尝试的失败回调（换号重试前的
// 同步回调：失败即时归因，先于下一次决策与最终结果到达）
func waitIntermediateFailure(t *testing.T, e *testEnv, wantAccount int64) map[string]interface{} {
	t.Helper()
	cb := e.waitCallback(t)
	if cb["status"] != "failed" {
		t.Fatalf("中间回调 status = %v, 期望 failed", cb["status"])
	}
	if cb["account_id"] != float64(wantAccount) {
		t.Fatalf("中间回调 account_id = %v, 期望 %d", cb["account_id"], wantAccount)
	}
	return cb
}

// policyZero 返回全禁用的重试策略（无心跳/无超时，测试稳定）
func policyZero() RetryPolicy {
	return RetryPolicy{}
}

// TestPing 验证健康检查路由
func TestPing(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/open/llm/ping", nil)
	Handler(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("ping 状态码 = %d, 期望 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "llm relay ready") {
		t.Fatalf("ping 响应异常: %s", w.Body.String())
	}
}

// TestHandlerStripsUTF8BOM 客户端请求体带 UTF-8 BOM（Windows 工具链偶发）时
// 入口剥离：未剥离会致 json.Marshal(RawMessage) 校验失败，决策请求静默变为
// 空 body，决策端只能报出误导性的「messages 参数不能为空」
func TestHandlerStripsUTF8BOM(t *testing.T) {
	var dispatchBody []byte
	setup(t, func(w http.ResponseWriter, r *http.Request) bool {
		dispatchBody, _ = io.ReadAll(r.Body)
		// 决策拒绝即可结束流程（无需真实转发）
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"code": 400, "message": "for-bom-test", "data": nil,
		})
		return true
	})

	raw := []byte(`{"model":"bom-model","input":"你好"}`)
	body := append([]byte{0xEF, 0xBB, 0xBF}, raw...)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/responses", bytes.NewReader(body))
	Handler(w, r)

	if len(dispatchBody) == 0 {
		t.Fatal("决策请求体为空（Marshal 失败被静默吞掉）")
	}
	if bytes.HasPrefix(dispatchBody, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("决策请求体仍带 BOM: %q", dispatchBody)
	}
	var decision struct {
		Model string          `json:"model"`
		Body  json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(dispatchBody, &decision); err != nil {
		t.Fatalf("决策请求体不是合法 JSON: %v: %q", err, dispatchBody)
	}
	if decision.Model != "bom-model" {
		t.Fatalf("决策请求 model = %q, 期望 bom-model", decision.Model)
	}
	if string(decision.Body) != string(raw) {
		t.Fatalf("决策请求 body = %q, 期望 %q", decision.Body, raw)
	}
}

// TestRelayRejectsInvalidJSONBody 客户端请求体不是合法 JSON（如 UTF-16 编码）
// 时显式报错，而非静默发送空决策请求体（决策端将报出误导性错误）
func TestRelayRejectsInvalidJSONBody(t *testing.T) {
	setup(t)

	// UTF-16 LE 字节流：对 JSON 解析器而言是非法输入
	body := []byte{0x7B, 0x00, 0x22, 0x00, 0x6D, 0x00}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/responses", bytes.NewReader(body))
	Handler(w, r)

	got := w.Body.String()
	if !strings.Contains(got, "客户端请求体不是合法 JSON") {
		t.Fatalf("客户端未收到非法 JSON 报错: %s", got)
	}
}

// TestRelayStreamSuccess 正常流式转发：usage 提取 + 回调 success
func TestRelayStreamSuccess(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 验证上游请求携带决策下发的凭证
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("上游 Authorization = %q, 期望 Bearer test-key", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer test-key", policyZero(), 2))

	body := `{"model":"test-model","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	respBody := w.Body.String()
	if !strings.Contains(respBody, "你好") {
		t.Fatalf("客户端未收到上游内容: %s", respBody)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Fatalf("客户端未收到结束标记: %s", respBody)
	}

	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
	if cb["account_id"] != float64(2) {
		t.Fatalf("回调 account_id = %v, 期望 2", cb["account_id"])
	}
	if cb["total_tokens"] != float64(150) {
		t.Fatalf("回调 total_tokens = %v, 期望 150", cb["total_tokens"])
	}
}

// TestRelayRetryOn429 上游 429 → 未写客户端 → 中间尝试同步回调归因 → 换号重试成功
func TestRelayRetryOn429(t *testing.T) {
	origBackoff := rateLimitBackoff
	rateLimitBackoff = 10 * time.Millisecond // 缩短喘息等待
	t.Cleanup(func() { rateLimitBackoff = origBackoff })

	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 两种凭证区分两次尝试：第一次 429，第二次成功
		if r.Header.Get("Authorization") == "Bearer bad-key" {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer bad-key", policy, 1),
		func(w http.ResponseWriter, r *http.Request) bool {
			// 第二次决策应携带 tried_account_ids=[1]
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			tried, _ := payload["tried_account_ids"].([]interface{})
			if len(tried) != 1 || tried[0] != float64(1) {
				t.Errorf("第二次决策 tried_account_ids = %v, 期望 [1]", tried)
			}
			return dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer good-key", policy, 3)(w, r)
		},
	)

	body := `{"model":"test-model","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("重试后客户端应收到成功内容: %s", w.Body.String())
	}
	// 中间尝试同步回调：429 即时归因（PHP 侧冷却该 Key），先于第二次决策到达
	intermediate := env.waitCallback(t)
	if intermediate["account_id"] != float64(1) || intermediate["error_kind"] != "rate_limit" {
		t.Fatalf("中间回调 account_id/error_kind = %v/%v, 期望 1/rate_limit",
			intermediate["account_id"], intermediate["error_kind"])
	}
	if intermediate["status"] != "failed" {
		t.Fatalf("中间回调 status = %v, 期望 failed", intermediate["status"])
	}
	// 最终成功回调：第二个账号
	final := env.waitCallback(t)
	if final["account_id"] != float64(3) {
		t.Fatalf("最终回调 account_id = %v, 期望 3（第二个账号）", final["account_id"])
	}
	if final["status"] != "success" {
		t.Fatalf("最终回调 status = %v, 期望 success", final["status"])
	}
}

// TestRelayRetryOn402 上游 402（额度不足）→ 未写客户端 → 中间尝试同步回调
// 归因为 quota → 换号重试成功。回归多 Key 场景 402 直接终止的缺陷：
// 402 与 429/401/403 同属单 Key 语义，须可换号重试
func TestRelayRetryOn402(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 两种凭证区分两次尝试：第一次 402，第二次成功
		if r.Header.Get("Authorization") == "Bearer no-quota-key" {
			w.WriteHeader(http.StatusPaymentRequired)
			fmt.Fprint(w, `{"error":{"message":"Token 余额不足"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer no-quota-key", policy, 1),
		func(w http.ResponseWriter, r *http.Request) bool {
			// 第二次决策应携带 tried_account_ids=[1]（已试 Key 排除清单）
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			tried, _ := payload["tried_account_ids"].([]interface{})
			if len(tried) != 1 || tried[0] != float64(1) {
				t.Errorf("第二次决策 tried_account_ids = %v, 期望 [1]", tried)
			}
			return dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer good-key", policy, 3)(w, r)
		},
	)

	body := `{"model":"test-model","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("重试后客户端应收到成功内容: %s", w.Body.String())
	}
	// 中间尝试同步回调：402 归因为 quota（PHP 侧据此做 Key/池级冷却）
	intermediate := waitIntermediateFailure(t, env, 1)
	if intermediate["error_kind"] != "quota" {
		t.Fatalf("中间回调 error_kind = %v, 期望 quota", intermediate["error_kind"])
	}
	if !strings.Contains(fmt.Sprint(intermediate["error_message"]), "上游返回 402") {
		t.Fatalf("中间回调 error_message = %v, 期望含「上游返回 402」", intermediate["error_message"])
	}
	// 最终成功回调：第二个账号
	final := env.waitCallback(t)
	if final["account_id"] != float64(3) {
		t.Fatalf("最终回调 account_id = %v, 期望 3（第二个账号）", final["account_id"])
	}
	if final["status"] != "success" {
		t.Fatalf("最终回调 status = %v, 期望 success", final["status"])
	}
}

// TestRelay429MessageAnnotation 429 错误消息语义化注解：客户端错误 chunk
// 与日志回调均携带 TPM/RPM 归类与恢复指引
func TestRelay429MessageAnnotation(t *testing.T) {
	origBackoff := rateLimitBackoff
	rateLimitBackoff = 10 * time.Millisecond
	t.Cleanup(func() { rateLimitBackoff = origBackoff })

	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"[insufficient_quota] inference exceeds tpm/rpm limit"}}`)
	})

	// 两次尝试均 429（MaxRetries=1：1 次原始 + 1 次重试）→ 终局透出注解后的错误
	policy := policyZero()
	policy.MaxRetries = 1
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 2),
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	for _, want := range []string{"账号分钟级配额限流", "Key 已自动冷却", "扩充 Key 池"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("客户端错误 chunk 应含注解文案 %q: %s", want, w.Body.String())
		}
	}
	cb := env.waitCallback(t)
	if cb["error_kind"] != "rate_limit" {
		t.Fatalf("回调 error_kind = %v, 期望 rate_limit", cb["error_kind"])
	}
	if !strings.Contains(fmt.Sprint(cb["error_message"]), "账号分钟级配额限流") {
		t.Fatalf("回调 error_message 应含注解文案: %v", cb["error_message"])
	}
}

// TestRelayAuthError429Kind 上游 401 不可恢复时回调 error_kind=auth；429 时 error_kind=rate_limit
func TestRelayAuthError429Kind(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid token"}}`)
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer bad", policyZero(), 1))

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	cb := env.waitCallback(t)
	if cb["error_kind"] != "auth" {
		t.Fatalf("回调 error_kind = %v, 期望 auth", cb["error_kind"])
	}
	if !strings.Contains(fmt.Sprint(cb["error_message"]), "invalid token") {
		t.Fatalf("回调 error_message 应含上游错误信息: %v", cb["error_message"])
	}
}

// TestRelayNonStream 非流式请求整体透传并提取 usage
func TestRelayNonStream(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", policyZero(), 1))

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), `"content":"hi"`) {
		t.Fatalf("非流式响应未透传: %s", w.Body.String())
	}
	cb := env.waitCallback(t)
	if cb["total_tokens"] != float64(15) {
		t.Fatalf("非流式回调 total_tokens = %v, 期望 15", cb["total_tokens"])
	}
}

// parseJSONResp 解析客户端收到的 JSON 响应体（测试辅助）
func parseJSONResp(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(w.Body.String()), &resp); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v, body=%s", err, w.Body.String())
	}
	return resp
}

// TestRelayNonStreamAggregatesSSE 上游强制返回 SSE（华为 DevEco 仅支持
// stream=true），客户端按非流式发起：Go 聚合为标准 chat.completion JSON——
// content/reasoning_content 拼接、取首 chunk 的 id/model 与 role、usage 取
// 收尾空 choices chunk、回调落库
func TestRelayNonStreamAggregatesSSE(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		flusher := w.(http.Flusher)
		// 首 chunk：role + 空 content + reasoning_content（华为样本特征：带 id: 行）
		fmt.Fprint(w, "id: 1\n")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"GLM5_1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"reasoning_content\":\"思考\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "id: 2\n")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "id: 3\n")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"，世界\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "id: 4\n")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		// 收尾空 choices + usage
		fmt.Fprint(w, "id: 5\n")
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-x\",\"choices\":[],\"usage\":{\"completion_tokens\":7,\"prompt_tokens\":3,\"total_tokens\":10}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", policyZero(), 1))

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, 期望 application/json", ct)
	}
	resp := parseJSONResp(t, w)
	if resp["object"] != "chat.completion" {
		t.Fatalf("object = %v, 期望 chat.completion", resp["object"])
	}
	if resp["id"] != "chatcmpl-x" || resp["model"] != "GLM5_1" {
		t.Fatalf("id/model = %v/%v, 期望 chatcmpl-x/GLM5_1", resp["id"], resp["model"])
	}
	choices, _ := resp["choices"].([]interface{})
	if len(choices) != 1 {
		t.Fatalf("choices 长度 = %d, 期望 1", len(choices))
	}
	choice := choices[0].(map[string]interface{})
	if choice["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v, 期望 stop", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]interface{})
	if msg["role"] != "assistant" || msg["content"] != "你好，世界" || msg["reasoning_content"] != "思考" {
		t.Fatalf("message = %v, 期望 role/content/reasoning_content 拼接正确", msg)
	}
	usage := resp["usage"].(map[string]interface{})
	if usage["prompt_tokens"] != float64(3) || usage["completion_tokens"] != float64(7) || usage["total_tokens"] != float64(10) {
		t.Fatalf("usage = %v, 期望取收尾 chunk 的 3/7/10", usage)
	}

	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
	if cb["total_tokens"] != float64(10) {
		t.Fatalf("回调 total_tokens = %v, 期望 10", cb["total_tokens"])
	}
}

// TestRelayNonStreamAggregatesToolCalls tool_calls 增量按 index 合并：
// function.name / arguments 分片拼接，finish_reason=tool_calls
func TestRelayNonStreamAggregatesToolCalls(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"type\":\"function\",\"function\":{\"name\":\"get_\",\"arguments\":\"{\\\"ci\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"weather\",\"arguments\":\"ty\\\":\\\"北京\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", policyZero(), 1))

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	resp := parseJSONResp(t, w)
	choice := resp["choices"].([]interface{})[0].(map[string]interface{})
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %v, 期望 tool_calls", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]interface{})
	calls, _ := msg["tool_calls"].([]interface{})
	if len(calls) != 1 {
		t.Fatalf("tool_calls 长度 = %d, 期望 1", len(calls))
	}
	call := calls[0].(map[string]interface{})
	if call["id"] != "call_a" || call["type"] != "function" {
		t.Fatalf("tool_call id/type = %v/%v, 期望 call_a/function", call["id"], call["type"])
	}
	fn := call["function"].(map[string]interface{})
	if fn["name"] != "get_weather" {
		t.Fatalf("function.name = %v, 期望 get_weather（分片拼接）", fn["name"])
	}
	if fn["arguments"] != `{"city":"北京"}` {
		t.Fatalf("function.arguments = %v, 期望 {\"city\":\"北京\"}", fn["arguments"])
	}

	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
}

// TestRelayNonStreamZeroOutputRetries 非流式聚合零实质内容（上游 200 但 SSE
// 无内容）→ 判 failed 且未写客户端 → 换号重试成功（对齐流式零输出语义）
func TestRelayNonStreamZeroOutputRetries(t *testing.T) {
	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			// 第一次：200 + SSE 但仅空 delta，无任何实质内容
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"c2\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 2),
	)

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	resp := parseJSONResp(t, w)
	choice := resp["choices"].([]interface{})[0].(map[string]interface{})
	if choice["message"].(map[string]interface{})["content"] != "ok" {
		t.Fatalf("零输出换号重试后应收到聚合内容: %s", w.Body.String())
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["status"] != "success" || cb["account_id"] != float64(2) {
		t.Fatalf("回调 status = %v account_id = %v, 期望 success/2", cb["status"], cb["account_id"])
	}
}

// ---------- chat 端点协议转换（response_transform=responses_to_chat） ----------

// dispatchChatTransformOK 构造带 responses_to_chat 响应转换指令的成功决策
func dispatchChatTransformOK(t *testing.T, upstreamURL, model string, accountID int64) func(w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) bool {
		data := DispatchData{
			DispatchID:             fmt.Sprintf("dp-%d", time.Now().UnixNano()),
			UpstreamURL:            upstreamURL,
			AuthHeader:             "Bearer tk",
			AccountID:              accountID,
			RetryPolicy:            policyZero(),
			ResponseTransform:      responseTransformChat,
			ResponseTransformModel: model,
		}
		writeJSON(w, http.StatusOK, dispatchResponse{Code: 0, Message: "success", Data: &data})
		return true
	}
}

// TestChatTransformStream chat 端点流式转换：上游 Responses SSE 实时回转为
// Chat SSE——角色首包/reasoning_content/content/finish chunk + [DONE]、
// model 回写真实 model_id、usage 归一化后随 finish chunk 下发并落库
func TestChatTransformStream(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_abc\",\"created_at\":1730000000,\"model\":\"gpt-5.6-terra\"}}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"思考中\"}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"你好\"}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"，世界\"}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\"}],\"usage\":{\"input_tokens\":35,\"output_tokens\":15,\"total_tokens\":50}}}\n\n")
		fl.Flush()
	})

	env := setup(t, dispatchChatTransformOK(t, upstream.URL+"/v1/responses", "gpt-5.6-terra", 3))

	body := `{"model":"gpt-5.6-terra","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	got := w.Body.String()
	for _, want := range []string{
		"\"object\":\"chat.completion.chunk\"",
		"\"role\":\"assistant\"",
		"\"reasoning_content\":\"思考中\"",
		"\"content\":\"你好\"",
		"\"content\":\"，世界\"",
		"\"finish_reason\":\"stop\"",
		"\"model\":\"gpt-5.6-terra\"",
		"\"prompt_tokens\":35",
		"data: [DONE]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("客户端未收到 %q: %s", want, got)
		}
	}
	if strings.Contains(got, "response.output_text.delta") {
		t.Fatalf("Responses 原始事件不应透传给 chat 客户端: %s", got)
	}
	if n := strings.Count(got, "data: [DONE]"); n != 1 {
		t.Fatalf("[DONE] 出现 %d 次, 期望 1 次: %s", n, got)
	}

	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
	if cb["prompt_tokens"] != float64(35) || cb["completion_tokens"] != float64(15) || cb["total_tokens"] != float64(50) {
		t.Fatalf("回调 usage = %v/%v/%v, 期望 35/15/50",
			cb["prompt_tokens"], cb["completion_tokens"], cb["total_tokens"])
	}
}

// TestChatTransformNonStream chat 端点非流式：客户端按非流式发起（决策强制
// 上游流式），Go 把整块 Responses SSE 回转为 Chat SSE 后聚合为标准
// chat.completion JSON
func TestChatTransformNonStream(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_abc\",\"created_at\":1730000000,\"model\":\"gpt-5.6-terra\"}}\n\n")
		fmt.Fprint(w, "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"思考中\"}\n\n")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"你好，世界\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\"}],\"usage\":{\"input_tokens\":35,\"output_tokens\":15,\"total_tokens\":50}}}\n\n")
	})

	env := setup(t, dispatchChatTransformOK(t, upstream.URL+"/v1/responses", "gpt-5.6-terra", 3))

	body := `{"model":"gpt-5.6-terra","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, 期望 application/json", ct)
	}
	resp := parseJSONResp(t, w)
	if resp["object"] != "chat.completion" {
		t.Fatalf("object = %v, 期望 chat.completion", resp["object"])
	}
	if resp["model"] != "gpt-5.6-terra" {
		t.Fatalf("model = %v, 期望 gpt-5.6-terra", resp["model"])
	}
	choices, _ := resp["choices"].([]interface{})
	if len(choices) != 1 {
		t.Fatalf("choices 长度 = %d, 期望 1", len(choices))
	}
	choice := choices[0].(map[string]interface{})
	if choice["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v, 期望 stop", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]interface{})
	if msg["role"] != "assistant" || msg["content"] != "你好，世界" || msg["reasoning_content"] != "思考中" {
		t.Fatalf("message = %v, 期望 role/content/reasoning_content 聚合正确", msg)
	}
	usage := resp["usage"].(map[string]interface{})
	if usage["prompt_tokens"] != float64(35) || usage["completion_tokens"] != float64(15) {
		t.Fatalf("usage = %v, 期望 35/15", usage)
	}

	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
}

// TestChatTransformToolCalls chat 端点工具调用转换：function_call 事件映射为
// tool_calls chunk（index 从 0 起、id/name 首包、arguments 增量跟随），
// finish_reason 推导为 tool_calls
func TestChatTransformToolCalls(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_tc\",\"created_at\":1730000000,\"model\":\"gpt-5.6-terra\"}}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"get_weather\"}}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"city\\\":\\\"北京\\\"}\"}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\"}],\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n")
		fl.Flush()
	})

	env := setup(t, dispatchChatTransformOK(t, upstream.URL+"/v1/responses", "gpt-5.6-terra", 3))

	body := `{"model":"gpt-5.6-terra","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	got := w.Body.String()
	for _, want := range []string{
		"\"tool_calls\"",
		"\"id\":\"call_1\"",
		"\"name\":\"get_weather\"",
		"\"index\":0",
		"\"arguments\":\"{\\\"city\\\":\\\"北京\\\"}\"",
		"\"finish_reason\":\"tool_calls\"",
		"data: [DONE]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("客户端未收到 %q: %s", want, got)
		}
	}

	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
}

// TestResponsesToChatConverterEOF 兜底与失败路径：上游流截断（未见结束事件）
// 时 Reader 补发结束 chunk 与 [DONE]；上游显式 failed 事件时错误信息以
// content 透出；[DONE] 仅随结束批追加一次
func TestResponsesToChatConverterEOF(t *testing.T) {
	// 场景一：显式 failed 事件 → 错误 content + finish chunk + [DONE]
	src := strings.NewReader(
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"created_at\":1,\"model\":\"m\"}}\n\n" +
			"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"配额耗尽\"}}}\n\n")
	got, err := io.ReadAll(newResponsesToChatReader(src, "gpt-5.6-terra"))
	if err != nil {
		t.Fatalf("读取转换流失败: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		"[上游错误] 配额耗尽",
		"\"finish_reason\":\"stop\"",
		"data: [DONE]",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("failed 转换未包含 %q: %s", want, s)
		}
	}
	if n := strings.Count(s, "data: [DONE]"); n != 1 {
		t.Fatalf("failed 流 [DONE] 出现 %d 次, 期望 1 次: %s", n, s)
	}

	// 场景二：流截断（无结束事件）→ EOF 兜底补 finish chunk 与 [DONE]
	src2 := strings.NewReader(
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"r2\",\"created_at\":1,\"model\":\"m\"}}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"半截\"}\n\n")
	got2, err := io.ReadAll(newResponsesToChatReader(src2, "m2"))
	if err != nil {
		t.Fatalf("读取截断转换流失败: %v", err)
	}
	s2 := string(got2)
	if !strings.Contains(s2, "\"content\":\"半截\"") {
		t.Fatalf("截断流未透出已收内容: %s", s2)
	}
	if !strings.Contains(s2, "\"finish_reason\":\"stop\"") || !strings.Contains(s2, "data: [DONE]") {
		t.Fatalf("截断流未补发结束 chunk 与 [DONE]: %s", s2)
	}
	if n := strings.Count(s2, "data: [DONE]"); n != 1 {
		t.Fatalf("截断流 [DONE] 出现 %d 次, 期望 1 次: %s", n, s2)
	}
}

// TestIsSSEPayload 上游载荷 SSE 判定：Content-Type 命中 / data:、id: 前缀兜底 /
// 完整 JSON 与空载荷不命中
func TestIsSSEPayload(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		data        string
		want        bool
	}{
		{"content-type 命中", "text/event-stream; charset=utf-8", `{"choices":[]}`, true},
		{"data 前缀兜底", "application/json", "data: {\"choices\":[]}\n\n", true},
		{"id 前缀兜底", "", "id: 1\ndata: {}\n\n", true},
		{"完整 JSON 不命中", "application/json", `{"choices":[]}`, false},
		{"空载荷不命中", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSSEPayload(c.contentType, []byte(c.data)); got != c.want {
				t.Fatalf("isSSEPayload(%q, %q) = %v, 期望 %v", c.contentType, c.data, got, c.want)
			}
		})
	}
}

// TestRelayBodyOverride 决策下发 body_override 时整体替换原始请求体转发上游
func TestRelayBodyOverride(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		_ = json.Unmarshal(raw, &payload)
		// 上游应收到改写后的真实 model_id，而非客户端原始别名
		if payload["model"] != "deepseek/deepseek-v4-flash" {
			t.Errorf("上游收到 model = %v, 期望改写后的真实 model_id", payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})

	setup(t, func(w http.ResponseWriter, r *http.Request) bool {
		data := DispatchData{
			DispatchID:   "dp-override",
			UpstreamURL:  upstream.URL + "/v1/chat/completions",
			AuthHeader:   "Bearer k",
			AccountID:    1,
			RetryPolicy:  policyZero(),
			BodyOverride: json.RawMessage(`{"model":"deepseek/deepseek-v4-flash","stream":false,"messages":[]}`),
		}
		writeJSON(w, http.StatusOK, dispatchResponse{Code: 0, Message: "success", Data: &data})
		return true
	})

	body := `{"model":"deepseek-v4-flash-loc","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), `"content":"hi"`) {
		t.Fatalf("响应未透传: %s", w.Body.String())
	}
}

// TestRelayDispatchFailure 决策接口失败（无可用账号）→ 错误 chunk + [DONE]
func TestRelayDispatchFailure(t *testing.T) {
	env := setup(t, func(w http.ResponseWriter, r *http.Request) bool {
		writeJSON(w, http.StatusOK, dispatchResponse{Code: 1, Message: "暂无可用账号"})
		return true
	})

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	respBody := w.Body.String()
	if !strings.Contains(respBody, "暂无可用账号") {
		t.Fatalf("客户端应收到决策失败信息: %s", respBody)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Fatalf("错误响应应以 [DONE] 结尾: %s", respBody)
	}
	// 决策失败时 lastDispatch 为 nil，不应有回调
	select {
	case p := <-env.callbacks:
		t.Fatalf("决策失败不应产生日志回调: %v", p)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRelayLocalResponse 决策返回本地短路响应（models 管理端点）→ 直接写回，不连上游、不回调
func TestRelayLocalResponse(t *testing.T) {
	upstreamHit := false
	sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		fmt.Fprint(w, `{"should":"not-called"}`)
	})

	env := setup(t, func(w http.ResponseWriter, r *http.Request) bool {
		data := DispatchData{
			LocalResponse: &LocalResponse{
				Status: http.StatusOK,
				Body:   json.RawMessage(`{"object":"list","data":[{"id":"glm-5.2","object":"model"}]}`),
			},
		}
		writeJSON(w, http.StatusOK, dispatchResponse{Code: 0, Message: "success", Data: &data})
		return true
	})

	w := httptest.NewRecorder()
	// 真实 GET models 请求无 body：覆盖决策请求空 body 以 JSON null 占位的
	// 场景（RawMessage(空) 序列化报错会导致短路前即失败）
	r := httptest.NewRequest(http.MethodGet, "/open/llm/cline/v1/models", nil)
	relay(w, r, "cline", "v1/models", nil)

	if got := w.Body.String(); !strings.Contains(got, `"id":"glm-5.2"`) || strings.Contains(got, "not-called") {
		t.Fatalf("客户端应收到本地短路响应体: %s", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, 期望 application/json", ct)
	}
	if upstreamHit {
		t.Fatal("本地短路不应请求上游")
	}
	// 短路不产生日志回调
	select {
	case p := <-env.callbacks:
		t.Fatalf("本地短路不应产生日志回调: %v", p)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRelayNoRetryAfterWrite 已向客户端写出数据后上游断开 → 不换号重试
func TestRelayNoRetryAfterWrite(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"部分\"}}]}\n\n")
		flusher.Flush()
		// 写出数据后强制断开（Hijack 模拟半开）
		conn, buf, _ := w.(http.Hijacker).Hijack()
		_ = buf.Flush()
		conn.Close()
	})

	policy := policyZero()
	policy.MaxRetries = 3
	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", policy, 1))

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	env.mu.Lock()
	calls := env.dispatchCalls
	env.mu.Unlock()
	if calls != 1 {
		t.Fatalf("决策调用次数 = %d, 期望 1（已写客户端不可重试）", calls)
	}
}

// TestRelayRetryOnConnectError 上游连接失败（端口拒绝连接）→ 未写客户端 → 换号重试成功
func TestRelayRetryOnConnectError(t *testing.T) {
	// 构造一个必然拒绝连接的地址：监听后立即关闭
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	deadURL := fmt.Sprintf("http://%s/v1/chat/completions", l.Addr().String())
	l.Close()

	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, deadURL, "Bearer k1", policy, 1),
		func(w http.ResponseWriter, r *http.Request) bool {
			// 第二次决策应携带 tried_account_ids=[1]
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			tried, _ := payload["tried_account_ids"].([]interface{})
			if len(tried) != 1 || tried[0] != float64(1) {
				t.Errorf("第二次决策 tried_account_ids = %v, 期望 [1]", tried)
			}
			return dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 3)(w, r)
		},
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("连接失败换号重试后客户端应收到成功内容: %s", w.Body.String())
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["account_id"] != float64(3) {
		t.Fatalf("回调 account_id = %v, 期望 3（第二个账号）", cb["account_id"])
	}
}

// TestRelayRetryOnFirstByteTimeout 上游首字节超时（连接建立但迟迟不响应）→ 未写客户端 → 换号重试成功
func TestRelayRetryOnFirstByteTimeout(t *testing.T) {
	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			// 第一次：连接成功后长时间不吐首字节（有界等待避免测试悬挂），
			// Go 侧 ResponseHeaderTimeout 到期即连接失败并换号重试
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	policy.FirstByteTimeoutMs = 100
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 3),
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("首字节超时换号重试后客户端应收到成功内容: %s", w.Body.String())
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["account_id"] != float64(3) {
		t.Fatalf("回调 account_id = %v, 期望 3", cb["account_id"])
	}
}

// TestRelayHeaderTimeoutMsDrivesResponseHeaderTimeout header_timeout_ms 驱动等响应头预算：
// first_byte_timeout_ms 不再兼任等头超时——上游延迟吐头（>first_byte、<header_timeout）应成功
func TestRelayHeaderTimeoutMsDrivesResponseHeaderTimeout(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 延迟 300ms 才吐响应头：若等头预算误用 first_byte_timeout_ms(60ms) 会提前断开
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.HeaderTimeoutMs = 8000  // 等头预算 8s
	policy.FirstByteTimeoutMs = 60 // 若被误用作等头预算则 60ms 即失败
	setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("延迟吐头(300ms)应成功（header_timeout_ms=8s 驱动等头预算）: %s", w.Body.String())
	}
}

// TestRelayRetryOnHeaderTimeoutMs header_timeout_ms 到期上游仍未吐响应头 → 连接失败 → 换号重试成功
func TestRelayRetryOnHeaderTimeoutMs(t *testing.T) {
	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			// 第一次：连接成功后长时间不吐响应头（有界等待避免测试悬挂），
			// Go 侧 ResponseHeaderTimeout（header_timeout_ms）到期即连接失败并换号重试
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	policy.HeaderTimeoutMs = 100 // 等头 100ms 到期即断（first_byte_timeout_ms 未参与）
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 3),
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("header_timeout_ms 超时换号重试后客户端应收到成功内容: %s", w.Body.String())
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["account_id"] != float64(3) {
		t.Fatalf("回调 account_id = %v, 期望 3", cb["account_id"])
	}
}

// TestRelayRetryOnIdleTimeout 心跳注释后上游空闲断开 → 注释行不算实质数据 → 仍可换号重试成功
func TestRelayRetryOnIdleTimeout(t *testing.T) {
	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			// 第一次：先下发响应头（进入流式转发循环），随后既不吐数据
			// 也不关闭（有界等待避免测试悬挂），由 Go 侧空闲超时判定半开
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	policy.FirstByteHeartbeatMs = 20 // 20ms 即发 ping 注释
	policy.ReadTimeoutMs = 150       // 150ms 无实质数据判半开
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 3),
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	respBody := w.Body.String()
	// 客户端已收到心跳注释（重试前的 SSE 注释行，合法且被客户端忽略）
	if !strings.Contains(respBody, ": ping") {
		t.Fatalf("重试前应已向客户端发送心跳注释: %s", respBody)
	}
	// 换号重试后仍成功：ping 注释不算实质数据，客户端无感接续新流
	if !strings.Contains(respBody, "ok") {
		t.Fatalf("空闲超时换号重试后客户端应收到成功内容: %s", respBody)
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["account_id"] != float64(3) {
		t.Fatalf("回调 account_id = %v, 期望 3", cb["account_id"])
	}
}

// TestRelayStripNonStandardFields 非标准字段过滤（对齐 PHP sanitizeChunk）：
// 上游 chunk 携带 provider/native_finish_reason/service_tier 时不透传客户端，
// 干净行原样透传（delta 空对象保真、思考字段保留）
func TestRelayStripNonStandardFields(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// 含全部非标准字段的 chunk（顶层 + choices 内）
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"provider\":\"cline\",\"service_tier\":\"default\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好\",\"reasoning\":\"思考\"},\"native_finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		// 干净 chunk + 空对象 delta（finish_reason chunk）——应原样透传
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", policyZero(), 1))

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	respBody := w.Body.String()
	for _, banned := range []string{`"provider"`, `"service_tier"`, `"native_finish_reason"`} {
		if strings.Contains(respBody, banned) {
			t.Fatalf("客户端输出不应含非标准字段 %s: %s", banned, respBody)
		}
	}
	// 实质内容与思考字段保留、空对象 delta 保真、[DONE] 收尾
	if !strings.Contains(respBody, "你好") || !strings.Contains(respBody, `"reasoning":"思考"`) {
		t.Fatalf("内容/思考字段应透传: %s", respBody)
	}
	if !strings.Contains(respBody, `"delta":{}`) {
		t.Fatalf("空对象 delta 应保真为 {}: %s", respBody)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Fatalf("应收到结束标记: %s", respBody)
	}
	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
}

// TestRelayZeroOutputFails 零字节流判失败（对齐 PHP「故障被当成成功」修复）：
// 上游 200 后立即关闭 → failed，未输出实质数据可换号重试，重试成功则客户端无感
func TestRelayZeroOutputFails(t *testing.T) {
	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			// 第一次：仅下发响应头立即关闭（零字节零行）
			w.Header().Set("Content-Type", "text/event-stream")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 2),
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("零输出换号重试后客户端应收到成功内容: %s", w.Body.String())
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["status"] != "success" || cb["account_id"] != float64(2) {
		t.Fatalf("回调 status = %v account_id = %v, 期望 success/2", cb["status"], cb["account_id"])
	}
}

// TestRelayZeroContentChunksFail 空内容流判失败：上游只发无实质内容的 chunk
// （空 delta、无 usage、无 finish_reason）即关闭，同属「故障被当成成功」盲区
func TestRelayZeroContentChunksFail(t *testing.T) {
	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			// 仅空 delta 初始 chunk，无任何实质内容
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 2),
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("空内容换号重试后客户端应收到成功内容: %s", w.Body.String())
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["status"] != "success" || cb["account_id"] != float64(2) {
		t.Fatalf("回调 status = %v account_id = %v, 期望 success/2", cb["status"], cb["account_id"])
	}
}

// ---------- SSE 流内错误事件识别（HTTP 200 + event: error） ----------

// huaweiStreamError 华为 DevEco 账号级故障的原文格式：HTTP 200 下发
// `id: 0` / 空 data / `event: error` / `data: {"error":{...,"code":"403"}}`
// （code 为字符串，不含任何实质内容 needle）
const huaweiStreamError = "id: 0\ndata: \n\nevent: error\n" +
	"data: {\"error\":{\"message\":\"服务器资源紧张，请稍后重试或配置自定义模型。\"," +
	"\"type\":\"ModelServiceRateLimit\",\"code\":\"403\"}}\n\n"

// shortRateLimitBackoff 缩短限流喘息退避，避免测试等待 1 秒
func shortRateLimitBackoff(t *testing.T) {
	t.Helper()
	orig := rateLimitBackoff
	rateLimitBackoff = 10 * time.Millisecond
	t.Cleanup(func() { rateLimitBackoff = orig })
}

// TestRelaySSEStreamErrorSwitchesAccount 流内错误须无感换号：上游以 HTTP 200 +
// SSE event:error 表达账号级故障时，不得误判为「上游零输出」（errKind=upstream
// 不冷却账号、无喘息退避、日志丢失真实文案），应归类 rate_limit 并换号重试
func TestRelaySSEStreamErrorSwitchesAccount(t *testing.T) {
	shortRateLimitBackoff(t)

	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		if atomic.AddInt32(&hits, 1) == 1 {
			fmt.Fprint(w, huaweiStreamError)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 2),
	)

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("流内错误换号重试后客户端应收到成功内容: %s", w.Body.String())
	}
	cb := waitIntermediateFailure(t, env, 1)
	if cb["error_kind"] != "rate_limit" {
		t.Fatalf("中间回调 error_kind = %v, 期望 rate_limit", cb["error_kind"])
	}
	if cb["http_code"] != float64(403) {
		t.Fatalf("中间回调 http_code = %v, 期望 403", cb["http_code"])
	}
	msg, _ := cb["error_message"].(string)
	if !strings.Contains(msg, "服务器资源紧张") {
		t.Fatalf("中间回调 error_message = %q, 期望携带上游真实文案", msg)
	}
	final := env.waitCallback(t)
	if final["status"] != "success" || final["account_id"] != float64(2) {
		t.Fatalf("回调 status = %v account_id = %v, 期望 success/2", final["status"], final["account_id"])
	}
}

// TestRelaySSEStreamErrorAfterContentFails 输出闸门已开（实质内容已写给客户端）
// 后上游才下发流内错误：无法换号，按失败归因收尾（不再把故障记成成功）
func TestRelaySSEStreamErrorAfterContentFails(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		fmt.Fprint(w, huaweiStreamError)
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1))

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "partial") {
		t.Fatalf("闸门已开时已写出的内容应保留: %s", w.Body.String())
	}
	cb := env.waitCallback(t)
	if cb["status"] != "failed" {
		t.Fatalf("回调 status = %v, 期望 failed", cb["status"])
	}
	if cb["error_kind"] != "rate_limit" || cb["http_code"] != float64(403) {
		t.Fatalf("回调 error_kind = %v http_code = %v, 期望 rate_limit/403", cb["error_kind"], cb["http_code"])
	}
	if cb["account_id"] != float64(1) {
		t.Fatalf("回调 account_id = %v, 期望 1（已输出不换号）", cb["account_id"])
	}
}

// TestRelayNonStreamSSEErrorSwitchesAccount 非流式聚合路径同样须识别流内错误：
// 客户端按非流式发起、上游（华为强制 stream=true）返回 SSE 流内错误时，
// 不得吞成「上游返回空响应」（errKind 为空被 PHP 归 connect，不冷却账号）
func TestRelayNonStreamSSEErrorSwitchesAccount(t *testing.T) {
	shortRateLimitBackoff(t)

	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		if atomic.AddInt32(&hits, 1) == 1 {
			fmt.Fprint(w, huaweiStreamError)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k2", policy, 2),
	)

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("非流式聚合换号重试后客户端应收到成功内容: %s", w.Body.String())
	}
	cb := waitIntermediateFailure(t, env, 1)
	if cb["error_kind"] != "rate_limit" || cb["http_code"] != float64(403) {
		t.Fatalf("中间回调 error_kind = %v http_code = %v, 期望 rate_limit/403", cb["error_kind"], cb["http_code"])
	}
	final := env.waitCallback(t)
	if final["status"] != "success" || final["account_id"] != float64(2) {
		t.Fatalf("回调 status = %v account_id = %v, 期望 success/2", final["status"], final["account_id"])
	}
}

// TestParseSSEErrorLine 流内错误载荷解析与归类的边界覆盖：
// 正常 chunk / 非 data 行 / [DONE] / "error":null 一律不得误判为错误
func TestParseSSEErrorLine(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantNil  bool
		wantCode int
		wantKind string
	}{
		{
			name:     "华为字符串 code + 服务级限流类型",
			line:     `data: {"error":{"message":"服务器资源紧张","type":"ModelServiceRateLimit","code":"403"}}`,
			wantCode: 403,
			wantKind: "rate_limit",
		},
		{
			name:     "数字 code 429",
			line:     `data: {"error":{"message":"too many requests","type":"rate_limit_exceeded","code":429}}`,
			wantCode: 429,
			wantKind: "rate_limit",
		},
		{
			name:     "无类型时按状态码归类 401",
			line:     `data: {"error":{"message":"invalid key","code":"401"}}`,
			wantCode: 401,
			wantKind: "auth",
		},
		{name: "正常内容 chunk", line: `data: {"choices":[{"delta":{"content":"hi"}}]}`, wantNil: true},
		{name: "error 为 null", line: `data: {"error":null,"choices":[]}`, wantNil: true},
		{name: "非 data 行", line: `event: error`, wantNil: true},
		{name: "流结束标记", line: `data: [DONE]`, wantNil: true},
		{name: "正文提及 error 字样", line: `data: {"choices":[{"delta":{"content":"error handling"}}]}`, wantNil: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseSSEErrorLine([]byte(c.line))
			if c.wantNil {
				if got != nil {
					t.Fatalf("parseSSEErrorLine(%q) = %+v, 期望 nil", c.line, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseSSEErrorLine(%q) = nil, 期望识别为流内错误", c.line)
			}
			if got.Code != c.wantCode {
				t.Fatalf("Code = %d, 期望 %d", got.Code, c.wantCode)
			}
			if kind := streamErrKind(got.Code, got.Type); kind != c.wantKind {
				t.Fatalf("streamErrKind = %q, 期望 %q", kind, c.wantKind)
			}
		})
	}
}

// TestParseSSEErrorLineNoTrailingNewline 上游未以换行收尾时，行缓冲残留的
// 错误行须由 flush 补扫捕获，否则漏判为「上游零输出」丢失真实归因
func TestParseSSEErrorLineNoTrailingNewline(t *testing.T) {
	sc := &lineScanner{}
	sc.feed([]byte("id: 0\nevent: error\ndata: {\"error\":{\"message\":\"资源紧张\"," +
		"\"type\":\"ModelServiceRateLimit\",\"code\":\"403\"}}"))
	if r := sc.flush(); r.streamErr == nil || r.streamErr.Code != 403 {
		t.Fatalf("flush 未捕获行缓冲残留的流内错误: %+v", r)
	}
}

// ---------- 客户端中断判定 ----------

// safeRecorder 并发安全的 ResponseWriter：转发协程写入的同时，
// 测试协程可轮询已写出的内容（httptest.Recorder 无锁，并发读会 race）
type safeRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
	hdr http.Header
}

func newSafeRecorder() *safeRecorder {
	return &safeRecorder{hdr: make(http.Header)}
}

func (s *safeRecorder) Header() http.Header { return s.hdr }

func (s *safeRecorder) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(b)
}

func (s *safeRecorder) WriteHeader(int) {}

func (s *safeRecorder) Flush() {}

// body 取当前已写出的响应体快照
func (s *safeRecorder) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitForBody 轮询等待已写出的响应体包含指定片段
func waitForBody(t *testing.T, rec *safeRecorder, want string, wait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.body(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待客户端收到 %q 超时，实际收到: %s", want, rec.body())
}

// TestRelayClientCloseAfterDoneIsSuccess 客户端收完 [DONE] 后立即断开、而上游
// FIN 尚未到达（公网 RTT）→ 应记为 success 而非 interrupted。
// 回归「已完整送达的成功响应被误记成客户端中断」的竞争窗口缺陷
func TestRelayClientCloseAfterDoneIsSuccess(t *testing.T) {
	release := make(chan struct{})
	defer close(release) // 先放行上游协程，再由 Cleanup 关闭测试服务器

	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		<-release // 挂住连接：dataCh 不关闭，唯一可命中的 select 分支是 ctx.Done
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", policyZero(), 7))

	body := `{"model":"m","stream":true,"messages":[]}`
	rec := newSafeRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions",
		strings.NewReader(body)).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		relay(rec, r, "cline", "chat/completions", []byte(body))
	}()

	waitForBody(t, rec, "[DONE]", 3*time.Second) // 确认响应已完整送达客户端
	cancel()                                     // 客户端关闭连接
	<-done

	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success（[DONE] 已送达客户端，不属中断）", cb["status"])
	}
	if cb["error_message"] != "" {
		t.Fatalf("回调 error_message = %v, 期望空", cb["error_message"])
	}
	if cb["error_kind"] != "" {
		t.Fatalf("回调 error_kind = %v, 期望空", cb["error_kind"])
	}
	if cb["total_tokens"] != float64(150) {
		t.Fatalf("回调 total_tokens = %v, 期望 150", cb["total_tokens"])
	}
	if cb["account_id"] != float64(7) {
		t.Fatalf("回调 account_id = %v, 期望 7", cb["account_id"])
	}
}

// TestRelayClientCancelMidStreamIsInterrupted 上游仅吐出部分内容、尚未发 [DONE]
// 时客户端取消 → 仍须记为 interrupted（防止 [DONE] 判定被过度放宽）
func TestRelayClientCancelMidStreamIsInterrupted(t *testing.T) {
	release := make(chan struct{})
	contentSent := make(chan struct{})
	defer close(release)

	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"部分\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(contentSent)
		<-release // 无 [DONE]、无 usage：流未结束
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", policyZero(), 3))

	body := `{"model":"m","stream":true,"messages":[]}`
	rec := newSafeRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions",
		strings.NewReader(body)).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		relay(rec, r, "cline", "chat/completions", []byte(body))
	}()

	waitForBody(t, rec, "部分", 3*time.Second)
	<-contentSent
	cancel()
	<-done

	cb := env.waitCallback(t)
	if cb["status"] != "interrupted" {
		t.Fatalf("回调 status = %v, 期望 interrupted", cb["status"])
	}
	if cb["error_kind"] != "client" {
		t.Fatalf("回调 error_kind = %v, 期望 client", cb["error_kind"])
	}
	msg, _ := cb["error_message"].(string)
	if !strings.Contains(msg, "客户端中断（输出") || !strings.Contains(msg, "后取消）") {
		t.Fatalf("回调 error_message = %q, 期望携带输出时长的中断文案", msg)
	}
}

// TestRelayClientCancelBeforeContentIsInterrupted 上游 prefill 阶段（响应头已到、
// 零实质内容）客户端取消 → interrupted，文案为不带后缀的裸「客户端中断」
func TestRelayClientCancelBeforeContentIsInterrupted(t *testing.T) {
	release := make(chan struct{})
	headerSent := make(chan struct{})
	defer close(release)

	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		close(headerSent)
		<-release // prefill 中，尚无任何数据行
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", policyZero(), 5))

	body := `{"model":"m","stream":true,"messages":[]}`
	rec := newSafeRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions",
		strings.NewReader(body)).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		relay(rec, r, "cline", "chat/completions", []byte(body))
	}()

	<-headerSent
	cancel()
	<-done

	cb := env.waitCallback(t)
	if cb["status"] != "interrupted" {
		t.Fatalf("回调 status = %v, 期望 interrupted", cb["status"])
	}
	if cb["error_kind"] != "client" {
		t.Fatalf("回调 error_kind = %v, 期望 client", cb["error_kind"])
	}
	if cb["error_message"] != "客户端中断" {
		t.Fatalf("回调 error_message = %v, 期望裸文案「客户端中断」（零实质内容）", cb["error_message"])
	}
	if cb["completion_tokens"] != nil {
		t.Fatalf("回调 completion_tokens = %v, 期望 null", cb["completion_tokens"])
	}
}

// TestScanLineDetectsDoneMarker [DONE] 识别：仅精确匹配流结束标记行，
// 内容中恰好含 [DONE] 字样的数据行不得误判
func TestScanLineDetectsDoneMarker(t *testing.T) {
	cases := []struct {
		line     string
		wantDone bool
	}{
		{"data: [DONE]", true},
		{"data:[DONE]", true},
		{"data: [DONE]  ", true},
		{"data: {\"choices\":[{\"delta\":{\"content\":\"[DONE]\"}}]}", false},
		{"data: {\"choices\":[{\"delta\":{\"content\":\"完成\"}}]}", false},
		{": ping", false},
		{"", false},
	}
	for _, c := range cases {
		if got := scanLine([]byte(c.line)).done; got != c.wantDone {
			t.Errorf("scanLine(%q).done = %v, 期望 %v", c.line, got, c.wantDone)
		}
	}
}

// TestLineScannerKeepsDoneWithUsage 同一批次内 usage 行整条覆盖累计结果时，
// done 标记不得丢失（覆盖 [DONE] 与 usage 同批到达的两种先后顺序）
func TestLineScannerKeepsDoneWithUsage(t *testing.T) {
	const usage = "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"
	const doneLine = "data: [DONE]\n\n"

	cases := []struct {
		name string
		in   string
	}{
		{"DONE 在 usage 之后", usage + doneLine},
		{"DONE 在 usage 之前", doneLine + usage},
	}
	for _, c := range cases {
		r := (&lineScanner{}).feed([]byte(c.in))
		if !r.done {
			t.Errorf("%s: done = false, 期望 true（覆盖累计结果时丢失结束标记）", c.name)
		}
		if r.completionTokens == nil || *r.completionTokens != 2 {
			t.Errorf("%s: completionTokens = %v, 期望 2", c.name, r.completionTokens)
		}
	}
}

// ---------- Responses API 支持 ----------

// TestParseChunkResponsesAPI Responses API 事件解析：增量 / 工具调用项 / 文本
// 完成态判为实质内容，response.completed 提取 usage 并置 done；生命周期事件无内容
func TestParseChunkResponsesAPI(t *testing.T) {
	cases := []struct {
		name        string
		line        string
		wantContent bool
		wantDone    bool
	}{
		{"文本增量", `data: {"type":"response.output_text.delta","delta":"你好"}`, true, false},
		{"推理增量", `data: {"type":"response.reasoning_summary_text.delta","delta":"思考"}`, true, false},
		{"空增量", `data: {"type":"response.output_text.delta","delta":""}`, false, false},
		{"工具调用项", `data: {"type":"response.output_item.added","item":{"type":"function_call","name":"f"}}`, true, false},
		{"消息项", `data: {"type":"response.output_item.added","item":{"type":"message","status":"in_progress"}}`, false, false},
		{"文本完成", `data: {"type":"response.output_text.done","text":"你好"}`, true, false},
		{"生命周期", `data: {"type":"response.created","response":{"id":"r1"}}`, false, false},
		{"进行中", `data: {"type":"response.in_progress"}`, false, false},
		{"完成事件", `data: {"type":"response.completed","response":{"output":[{"type":"message"}],"usage":{"input_tokens":35,"output_tokens":15,"total_tokens":50}}}`, true, true},
	}
	for _, c := range cases {
		r := scanLine([]byte(c.line))
		if r.hasContent != c.wantContent {
			t.Errorf("%s: hasContent = %v, 期望 %v", c.name, r.hasContent, c.wantContent)
		}
		if r.done != c.wantDone {
			t.Errorf("%s: done = %v, 期望 %v", c.name, r.done, c.wantDone)
		}
	}

	// 完成事件的 usage 提取（Responses 字段名 input_tokens/output_tokens/total_tokens）
	r := scanLine([]byte(`data: {"type":"response.completed","response":{"output":[{"type":"message"}],"usage":{"input_tokens":35,"output_tokens":15,"total_tokens":50}}}`))
	if r.promptTokens == nil || *r.promptTokens != 35 {
		t.Errorf("promptTokens = %v, 期望 35", r.promptTokens)
	}
	if r.completionTokens == nil || *r.completionTokens != 15 {
		t.Errorf("completionTokens = %v, 期望 15", r.completionTokens)
	}
	if r.totalTokens == nil || *r.totalTokens != 50 {
		t.Errorf("totalTokens = %v, 期望 50", r.totalTokens)
	}

	// 整包非流式对象（object=response）的 usage 提取
	raw := parseChunk([]byte(`{"object":"response","output":[{"type":"message"}],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}`))
	if raw.totalTokens == nil || *raw.totalTokens != 7 {
		t.Errorf("整包 totalTokens = %v, 期望 7", raw.totalTokens)
	}
}

// TestRelayResponsesStreamSuccess Responses API 流式转发：事件原样透传客户端、
// 完成事件 usage 落库、状态记为 success（修复 relay 把 Responses 误判为零输出）
func TestRelayResponsesStreamSuccess(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer resp-key" {
			t.Errorf("上游 Authorization = %q, 期望 Bearer resp-key", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"你好\"}\n\n")
		fl.Flush()
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\"}],\"usage\":{\"input_tokens\":35,\"output_tokens\":15,\"total_tokens\":50}}}\n\n")
		fl.Flush()
	})

	env := setup(t, dispatchOK(t, upstream.URL+"/v1/responses", "Bearer resp-key", policyZero(), 7))

	body := `{"model":"deepseek-v4.1-flash","stream":true,"input":"hi"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/responses", strings.NewReader(body))
	relay(w, r, "cline", "responses", []byte(body))

	got := w.Body.String()
	for _, want := range []string{"response.created", "response.output_text.delta", "你好", "response.completed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("客户端未收到 %q: %s", want, got)
		}
	}
	if strings.Contains(got, "上游零输出") {
		t.Fatalf("不应误判上游零输出: %s", got)
	}

	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
	if cb["account_id"] != float64(7) {
		t.Fatalf("回调 account_id = %v, 期望 7", cb["account_id"])
	}
	if cb["prompt_tokens"] != float64(35) || cb["completion_tokens"] != float64(15) || cb["total_tokens"] != float64(50) {
		t.Fatalf("回调 usage = %v/%v/%v, 期望 35/15/50",
			cb["prompt_tokens"], cb["completion_tokens"], cb["total_tokens"])
	}
}

// TestRelayResponsesZeroOutputFails Responses 流仅有生命周期事件（无增量 /
// 工具调用 / 完成事件）时仍判「上游零输出」并换号重试：放宽判定不得放过真故障
func TestRelayResponsesZeroOutputFails(t *testing.T) {
	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if atomic.AddInt32(&hits, 1) == 1 {
			fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n")
			fmt.Fprint(w, "event: response.in_progress\ndata: {\"type\":\"response.in_progress\"}\n\n")
			return
		}
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	})

	policy := policyZero()
	policy.MaxRetries = 2
	env := setup(t,
		dispatchOK(t, upstream.URL+"/v1/responses", "Bearer k1", policy, 1),
		dispatchOK(t, upstream.URL+"/v1/responses", "Bearer k2", policy, 2),
	)

	body := `{"model":"m","stream":true,"input":"hi"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/responses", strings.NewReader(body))
	relay(w, r, "cline", "responses", []byte(body))

	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("零输出换号重试后客户端应收到成功内容: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "response.created") {
		t.Fatalf("首次尝试的缓冲数据不应残留: %s", w.Body.String())
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["status"] != "success" || cb["account_id"] != float64(2) {
		t.Fatalf("回调 status = %v account_id = %v, 期望 success/2", cb["status"], cb["account_id"])
	}
}

// ---------- strip_tool_names：按名剔除幽灵工具调用（可选字段，兼容既有接入方） ----------

// dispatchStripOK 构造带工具名剔除名单的成功决策响应（stripNames 为空即历史行为）
func dispatchStripOK(t *testing.T, upstreamURL, authHeader string, accountID int64, stripNames []string) func(w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) bool {
		data := DispatchData{
			DispatchID:     fmt.Sprintf("dp-%d", time.Now().UnixNano()),
			UpstreamURL:    upstreamURL,
			AuthHeader:     authHeader,
			AccountID:      accountID,
			RetryPolicy:    policyZero(),
			StripToolNames: stripNames,
		}
		writeJSON(w, http.StatusOK, dispatchResponse{Code: 0, Message: "success", Data: &data})
		return true
	}
}

// TestRelayStripToolNamesStream 流式：名单内的入场券工具（read）增量被剔除，
// 正文与真实工具调用原样透传
func TestRelayStripToolNamesStream(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好\"}}]}\n\n")
		fl.Flush()
		// 同一增量内含入场券工具（read，index 0）与真实工具（get_weather，index 1）
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_read\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}},{\"index\":1,\"id\":\"call_w\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{}\"}}]}}]}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	})

	env := setup(t, dispatchStripOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", 1, []string{"read"}))

	body := `{"model":"m","stream":true,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	got := w.Body.String()
	if !strings.Contains(got, "你好") || !strings.Contains(got, "get_weather") {
		t.Fatalf("正文与真实工具调用应透传: %s", got)
	}
	if strings.Contains(got, "call_read") || strings.Contains(got, `"name":"read"`) {
		t.Fatalf("入场券工具调用应被剔除: %s", got)
	}
	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
}

// TestRelayStripToolNamesNonStreamAggregate 非流式聚合：仅剔除名单内工具，
// 真实工具调用经聚合后保留
func TestRelayStripToolNamesNonStreamAggregate(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_read\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}},{\"index\":1,\"id\":\"call_w\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\\\"北京\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	env := setup(t, dispatchStripOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", 1, []string{"read"}))

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	resp := parseJSONResp(t, w)
	msg := resp["choices"].([]interface{})[0].(map[string]interface{})["message"].(map[string]interface{})
	calls, _ := msg["tool_calls"].([]interface{})
	if len(calls) != 1 {
		t.Fatalf("tool_calls 长度 = %d, 期望 1（仅保留真实工具）", len(calls))
	}
	fn := calls[0].(map[string]interface{})["function"].(map[string]interface{})
	if fn["name"] != "get_weather" {
		t.Fatalf("保留的工具 = %v, 期望 get_weather", fn["name"])
	}
	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
}

// TestRelayStripToolNamesFullJSON 上游返回完整 JSON（非 SSE）时同样按名剔除
func TestRelayStripToolNamesFullJSON(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"你好","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	})

	env := setup(t, dispatchStripOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", 1, []string{"read"}))

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	got := w.Body.String()
	if !strings.Contains(got, "你好") {
		t.Fatalf("正文应保留: %s", got)
	}
	if strings.Contains(got, "call_read") || strings.Contains(got, `"name":"read"`) {
		t.Fatalf("完整 JSON 响应中的入场券工具应被剔除: %s", got)
	}
	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
}

// TestRelayStripToolNamesEmptyKeepsAll 名单为空（既有接入方，未下发新字段）时
// 工具调用零改动透传——验证「缺省即原行为」的向后兼容承诺
func TestRelayStripToolNamesEmptyKeepsAll(t *testing.T) {
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"你好","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	})

	env := setup(t, dispatchStripOK(t, upstream.URL+"/v1/chat/completions", "Bearer k", 1, nil))

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	got := w.Body.String()
	if !strings.Contains(got, "call_read") || !strings.Contains(got, `"name":"read"`) {
		t.Fatalf("名单为空时工具调用应原样透传: %s", got)
	}
	cb := env.waitCallback(t)
	if cb["status"] != "success" {
		t.Fatalf("回调 status = %v, 期望 success", cb["status"])
	}
}

// TestRelayStripToolNamesOnlyGhostRetries 上游仅回显入场券工具（无正文/思考）：
// 剔除后等价零输出 → failed 且 retryable → 换号重试成功
func TestRelayStripToolNamesOnlyGhostRetries(t *testing.T) {
	var hits int32
	upstream := sseServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if atomic.AddInt32(&hits, 1) == 1 {
			// 第一次：仅回显 read 入场券工具
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_read\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"c2\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	// 换号重试需可行的重试策略，另行构造带 MaxRetries 的决策
	policy := policyZero()
	policy.MaxRetries = 2
	stripOK := func(upstreamURL, authHeader string, accountID int64) func(w http.ResponseWriter, r *http.Request) bool {
		return func(w http.ResponseWriter, r *http.Request) bool {
			data := DispatchData{
				DispatchID:     fmt.Sprintf("dp-%d", time.Now().UnixNano()),
				UpstreamURL:    upstreamURL,
				AuthHeader:     authHeader,
				AccountID:      accountID,
				RetryPolicy:    policy,
				StripToolNames: []string{"read"},
			}
			writeJSON(w, http.StatusOK, dispatchResponse{Code: 0, Message: "success", Data: &data})
			return true
		}
	}
	env := setup(t,
		stripOK(upstream.URL+"/v1/chat/completions", "Bearer k1", 1),
		stripOK(upstream.URL+"/v1/chat/completions", "Bearer k2", 2),
	)

	body := `{"model":"m","stream":false,"messages":[]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/open/llm/cline/v1/chat/completions", strings.NewReader(body))
	relay(w, r, "cline", "chat/completions", []byte(body))

	resp := parseJSONResp(t, w)
	msg := resp["choices"].([]interface{})[0].(map[string]interface{})["message"].(map[string]interface{})
	if msg["content"] != "ok" {
		t.Fatalf("幽灵调用剔除后应换号重试并收到成功内容: %s", w.Body.String())
	}
	waitIntermediateFailure(t, env, 1)
	cb := env.waitCallback(t)
	if cb["status"] != "success" || cb["account_id"] != float64(2) {
		t.Fatalf("回调 status = %v account_id = %v, 期望 success/2", cb["status"], cb["account_id"])
	}
}

// TestSanitizeLineStripToolNamesFragmented 工具名分片累积：仍在名单前缀阶段
// 保守放行，累积成完整名单名后剔除；非名单名首片即放行
func TestSanitizeLineStripToolNamesFragmented(t *testing.T) {
	s := newStreamSanitizer([]string{"read"})

	// 首片 "re"（名单前缀）→ 保留
	l1 := s([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"re"}}]}}]}`))
	if !strings.Contains(string(l1), `"name":"re"`) {
		t.Fatalf("名单前缀分片应保留: %s", l1)
	}
	// 次片累积为 read → 剔除该 index 增量
	l2 := s([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"ad","arguments":"{}"}}]}}]}`))
	if strings.Contains(string(l2), "tool_calls") {
		t.Fatalf("累积为名单名后应剔除: %s", l2)
	}
	// 非名单名首片即放行
	l3 := s([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"name":"grep"}}]}}]}`))
	if !strings.Contains(string(l3), `"name":"grep"`) {
		t.Fatalf("非名单工具名应放行: %s", l3)
	}
}

// TestToolNameSetAndPrefix 名单规整与前缀判定：空串跳过、全空名单返回 nil（禁用）
func TestToolNameSetAndPrefix(t *testing.T) {
	if toolNameSet(nil) != nil || toolNameSet([]string{}) != nil || toolNameSet([]string{"", ""}) != nil {
		t.Fatal("空名单应返回 nil（禁用过滤）")
	}
	set := toolNameSet([]string{"read", ""})
	if _, ok := set["read"]; !ok || len(set) != 1 {
		t.Fatalf("名单应仅含 read（跳过空串）: %v", set)
	}
	if !isPrefixOfAny("re", set) || isPrefixOfAny("grep", set) {
		t.Fatal("前缀判定异常")
	}
}
