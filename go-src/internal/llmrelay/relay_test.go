package llmrelay

import (
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

	body := `{"model":"m","stream":false}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/open/llm/cline/v1/models", nil)
	relay(w, r, "cline", "v1/models", []byte(body))

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
