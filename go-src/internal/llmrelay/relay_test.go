package llmrelay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestRelayRetryOn429 上游 429 → 未写客户端 → 换号重试成功
func TestRelayRetryOn429(t *testing.T) {
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
	cb := env.waitCallback(t)
	if cb["account_id"] != float64(3) {
		t.Fatalf("回调 account_id = %v, 期望 3（第二个账号）", cb["account_id"])
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
