package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ============ 凭据与权限矩阵 ============

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := &Registry{tools: make(map[string]*Tool)}
	if err := RegisterBuiltinTools(reg); err != nil {
		t.Fatalf("注册内置工具失败: %v", err)
	}
	return reg
}

func TestCredentialEffectiveTools(t *testing.T) {
	reg := newTestRegistry(t)
	cred := &Credential{
		ID:       "key_1",
		Token:    "tok_1",
		ToolIDs:  []string{"SystemInfo", "FileRead"},
		Disabled: []string{"FileRead"}, // 显式禁用优先
	}
	effective := cred.EffectiveTools(reg.IDs())

	if !effective["SystemInfo"] {
		t.Error("SystemInfo 应可调用")
	}
	if effective["FileRead"] {
		t.Error("显式禁用的 FileRead 不应可调用")
	}
	if effective["SystemExec"] {
		t.Error("未授权工具不应可调用")
	}
}

func TestCredentialEffectiveToolsFiltersUnregistered(t *testing.T) {
	reg := newTestRegistry(t)
	cred := &Credential{ID: "k", Token: "t", ToolIDs: []string{"SystemInfo", "NoSuchTool"}}
	effective := cred.EffectiveTools(reg.IDs())

	if effective["NoSuchTool"] {
		t.Error("未注册的工具不应出现在可调用集")
	}
	if len(effective) != 1 {
		t.Errorf("应只剩 1 个有效工具，实际 %d", len(effective))
	}
}

func TestCredentialExpiry(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)

	never := &Credential{CreatedAt: "2020-01-01 00:00:00", ExpiresDays: -1}
	if never.IsExpired(now) {
		t.Error("ExpiresDays=-1 应永不过期")
	}

	fresh := &Credential{CreatedAt: "2026-10-20 12:00:00", ExpiresDays: 7}
	if fresh.IsExpired(now) {
		t.Error("7 天有效期内在有效期内")
	}

	stale := &Credential{CreatedAt: "2026-10-01 12:00:00", ExpiresDays: 7}
	if !stale.IsExpired(now) {
		t.Error("超过 7 天应判定过期")
	}

	// 创建时间无法解析 → 按失效处理（安全兜底）
	broken := &Credential{CreatedAt: "", ExpiresDays: 7}
	if !broken.IsExpired(now) {
		t.Error("创建时间非法应按失效处理")
	}
}

func TestCredentialStoreFindByToken(t *testing.T) {
	store := NewCredentialStore([]*Credential{
		{ID: "a", Token: "token-a", Active: true},
		{ID: "b", Token: "token-b", Active: true},
	})

	if got := store.FindByToken("token-b"); got == nil || got.ID != "b" {
		t.Error("应找到 token-b 对应凭据")
	}
	if got := store.FindByToken("token-x"); got != nil {
		t.Error("未知 token 应返回 nil")
	}
	if got := store.FindByToken(""); got != nil {
		t.Error("空 token 应返回 nil")
	}
	if store.Count() != 2 {
		t.Errorf("凭据数应为 2，实际 %d", store.Count())
	}
}

func TestCredentialStoreDeepCopy(t *testing.T) {
	src := []*Credential{{ID: "a", Token: "t", ToolIDs: []string{"X"}}}
	store := NewCredentialStore(src)

	// 修改原始切片不应影响已构建的凭据集
	src[0].ToolIDs[0] = "MUTATED"
	if store.Keys()[0].ToolIDs[0] != "X" {
		t.Error("凭据集应深拷贝，不受调用方后续修改影响")
	}
}

func TestCredentialStoreSkipsEmptyToken(t *testing.T) {
	store := NewCredentialStore([]*Credential{
		{ID: "a", Token: ""},
		nil,
		{ID: "b", Token: "ok"},
	})
	if store.Count() != 1 {
		t.Errorf("应跳过空 token 与 nil，实际保留 %d 条", store.Count())
	}
}

func TestDefaultToolIDsIsLowRiskOnly(t *testing.T) {
	reg := newTestRegistry(t)
	ids := DefaultToolIDs(reg)
	if len(ids) != len(reg.LowRiskIDs()) {
		t.Error("默认授权集应等于全部低风险工具")
	}
	for _, id := range ids {
		tool, _ := reg.Get(id)
		if tool.RiskLevel != RiskLow {
			t.Errorf("默认授权不应包含非低风险工具 %s", id)
		}
	}
}

func TestNormalizeAndComplementToolIDs(t *testing.T) {
	reg := newTestRegistry(t)
	registered := reg.IDs()

	granted := NormalizeToolIDs([]string{"SystemInfo", "FileRead", "NoSuchTool"}, registered)
	if len(granted) != 2 {
		t.Errorf("规范化后应剩 2 项，实际 %d", len(granted))
	}
	for _, id := range granted {
		if id == "NoSuchTool" {
			t.Error("未注册项应被剔除")
		}
	}

	disabled := ComplementToolIDs(granted, registered)
	if len(granted)+len(disabled) != len(registered) {
		t.Errorf("授权集(%d) 与禁用集(%d) 应构成互斥分区，合计 %d", len(granted), len(disabled), len(registered))
	}
	for _, id := range disabled {
		for _, g := range granted {
			if id == g {
				t.Errorf("工具 %s 同时出现在授权与禁用集", id)
			}
		}
	}
}

// ============ MCP 协议服务（HTTP 层） ============

// newTestServer 构造启用 MCP 的测试服务
func newTestServer(t *testing.T, configure func(cfg *Config)) (*Server, *Registry) {
	t.Helper()
	reg := newTestRegistry(t)
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.PathPrefix = "/test-mcp"
	cfg.IPAllowlist = []string{"*"}
	cfg.Credentials = []*Credential{
		{
			ID:          "key_1",
			Token:       "test-token",
			Name:        "测试凭据",
			ToolIDs:     append(reg.LowRiskIDs(), "FileDelete"),
			CreatedAt:   time.Now().Format("2006-01-02 15:04:05"),
			ExpiresDays: 30,
			Active:      true,
		},
	}
	if configure != nil {
		configure(cfg)
	}
	return NewServer(reg, cfg, "test", nil), reg
}

// doRPC 发起一次 MCP 请求
func doRPC(s *Server, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, s.MCPPath(), strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestMcpInitializeHandshake(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("握手应返回 200，实际 %d", rec.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应非 JSON: %v", err)
	}
	result, _ := resp["result"].(map[string]interface{})
	if result == nil {
		t.Fatal("握手应返回 result")
	}
	if result["protocolVersion"] != ProtocolVersion {
		t.Errorf("协议版本应为 %s，实际 %v", ProtocolVersion, result["protocolVersion"])
	}
	if result["serverInfo"] == nil {
		t.Error("应返回 serverInfo")
	}
	if result["instructions"] == nil {
		t.Error("应返回 instructions 指导 AI 使用")
	}
}

func TestMcpRejectsMissingToken(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doRPC(s, "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺少凭据应返回 401，实际 %d", rec.Code)
	}
}

func TestMcpRejectsInvalidToken(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doRPC(s, "wrong-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无效凭据应返回 401，实际 %d", rec.Code)
	}
}

func TestMcpRejectsInactiveCredential(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		cfg.Credentials[0].Active = false
	})
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("停用凭据应返回 401，实际 %d", rec.Code)
	}
}

func TestMcpRejectsExpiredCredential(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		cfg.Credentials[0].CreatedAt = "2020-01-01 00:00:00"
		cfg.Credentials[0].ExpiresDays = 1
	})
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("过期凭据应返回 401，实际 %d", rec.Code)
	}
}

func TestMcpRejectsIPNotInAllowlist(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		cfg.IPAllowlist = []string{"10.0.0.1"}
	})
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("IP 不在白名单应返回 403，实际 %d", rec.Code)
	}
}

func TestMcpRateLimit(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		cfg.RateLimitMax = 2
		cfg.RateLimitWindow = 60
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	doRPC(s, "test-token", body)
	doRPC(s, "test-token", body)
	rec := doRPC(s, "test-token", body)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超限应返回 429，实际 %d", rec.Code)
	}
}

func TestMcpDisabledService(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		cfg.Enabled = false
	})
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("未启用应返回 503，实际 %d", rec.Code)
	}
}

func TestMcpRejectsGetMethod(t *testing.T) {
	s, _ := newTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, s.MCPPath(), nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应返回 405，实际 %d", rec.Code)
	}
}

func TestMcpListToolsFiltersByCredential(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		// 仅授权 SystemInfo 一个工具
		cfg.Credentials[0].ToolIDs = []string{"SystemInfo"}
	})
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", rec.Code)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	result, _ := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})

	if len(tools) != 1 {
		t.Fatalf("仅授权 1 个工具，实际返回 %d 个", len(tools))
	}
	first, _ := tools[0].(map[string]interface{})
	if first["name"] != "SystemInfo" {
		t.Errorf("应返回 SystemInfo，实际 %v", first["name"])
	}
}

func TestMcpListToolsInjectsConfirmParam(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		cfg.Credentials[0].ToolIDs = []string{"FileDelete"} // 高风险，需确认
	})
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	result, _ := resp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	first, _ := tools[0].(map[string]interface{})
	schema, _ := first["inputSchema"].(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})

	if props == nil || props["confirm"] == nil {
		t.Fatal("需确认的工具应在 schema 中注入 confirm 参数")
	}
}

func TestMcpCallToolDeniedForUnauthorizedTool(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		cfg.Credentials[0].ToolIDs = []string{"SystemInfo"}
	})
	rec := doRPC(s, "test-token",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"SystemExec","arguments":{}}}`)

	body := rec.Body.String()
	if !strings.Contains(body, "未授权") {
		t.Fatalf("未授权工具应被拒绝，实际响应: %s", body)
	}
}

func TestMcpCallToolNotFound(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doRPC(s, "test-token",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"NoSuchTool","arguments":{}}}`)

	if !strings.Contains(rec.Body.String(), "工具不存在") {
		t.Fatalf("未知工具应报错，实际: %s", rec.Body.String())
	}
}

func TestMcpCallConfirmToolReturnsWaiting(t *testing.T) {
	s, _ := newTestServer(t, func(cfg *Config) {
		cfg.Credentials[0].ToolIDs = []string{"SystemExec"}
	})
	rec := doRPC(s, "test-token",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"SystemExec","arguments":{"command":"echo hi"}}}`)

	body := rec.Body.String()
	if !strings.Contains(body, "waiting_confirm") {
		t.Fatalf("高风险工具首次调用应返回 waiting_confirm，实际: %s", body)
	}
	if !strings.Contains(body, "instruction") {
		t.Error("等待确认响应应包含给 AI 的 instruction")
	}
}

func TestMcpCallUnknownMethod(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"unknown/method"}`)

	if !strings.Contains(rec.Body.String(), "-32601") {
		t.Fatalf("未知方法应返回 -32601，实际: %s", rec.Body.String())
	}
}

func TestMcpRejectsInvalidJSON(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doRPC(s, "test-token", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应返回 400，实际 %d", rec.Code)
	}
}

func TestMcpNotificationReturns202(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("通知类请求应返回 202，实际 %d", rec.Code)
	}
}

func TestMcpConfigNormalize(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := &Config{PathPrefix: "mcp-x", RateLimitMax: 0, Credentials: []*Credential{
		{ID: "k", Token: "t", ToolIDs: []string{"SystemInfo", "NoSuch"}, Disabled: []string{"NoSuch2"}},
	}}
	cfg.Normalize(reg)

	if cfg.PathPrefix != "/mcp-x" {
		t.Errorf("路径前缀应补全为 /mcp-x，实际 %s", cfg.PathPrefix)
	}
	if cfg.RateLimitMax != 300 {
		t.Errorf("限流上限应回退默认 300，实际 %d", cfg.RateLimitMax)
	}
	if len(cfg.Credentials[0].ToolIDs) != 1 {
		t.Errorf("授权集应剔除未注册项，实际 %d 项", len(cfg.Credentials[0].ToolIDs))
	}
	if len(cfg.Credentials[0].Disabled) != 0 {
		t.Errorf("禁用集中的未注册项应被剔除，实际 %d 项", len(cfg.Credentials[0].Disabled))
	}
}

func TestMcpPathPrefixTrailingSlashTrimmed(t *testing.T) {
	reg := newTestRegistry(t)
	cfg := &Config{PathPrefix: "/mcp-abc///"}
	cfg.Normalize(reg)
	if cfg.PathPrefix != "/mcp-abc" {
		t.Errorf("尾部斜杠应被去除，实际 %s", cfg.PathPrefix)
	}
}

func TestMcpReloadHotSwapsCredentials(t *testing.T) {
	s, reg := newTestServer(t, nil)

	// 重载前：旧凭据可用
	if rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`); rec.Code != 200 {
		t.Fatalf("重载前旧凭据应可用，实际 %d", rec.Code)
	}

	// 热重载：旧凭据移除，换入新凭据与端点前缀
	s.Reload(&BootstrapConfig{
		Enabled:     true,
		PathPrefix:  "/test-mcp-v2",
		IPAllowlist: []string{"*"},
		Credentials: []*Credential{
			{ID: "key_2", Token: "new-token", ToolIDs: []string{"SystemInfo"}, Active: true,
				CreatedAt: time.Now().Format("2006-01-02 15:04:05"), ExpiresDays: 30},
		},
	})

	if s.MCPPath() != "/test-mcp-v2/mcp" {
		t.Errorf("重载后端点应更新，实际 %s", s.MCPPath())
	}
	if s.CredentialCount() != 1 {
		t.Errorf("重载后凭据数应为 1，实际 %d", s.CredentialCount())
	}

	// 旧凭据失效、新凭据生效（旧 token 应 401）
	if rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`); rec.Code != 401 {
		t.Errorf("重载后旧凭据应 401，实际 %d", rec.Code)
	}
	if rec := doRPC(s, "new-token", `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`); rec.Code != 200 {
		t.Errorf("重载后新凭据应可用，实际 %d", rec.Code)
	}

	// 未注册工具应在 Normalize 时剔除（授权集只剩 SystemInfo）
	cred := s.Config().Credentials[0]
	if len(cred.ToolIDs) != 1 {
		t.Errorf("重载后授权集应剔除未注册项，实际 %v", cred.ToolIDs)
	}
	_ = reg
}

func TestMcpReloadDisablesService(t *testing.T) {
	s, _ := newTestServer(t, nil)
	if !s.Enabled() {
		t.Fatal("重载前应启用")
	}

	s.Reload(&BootstrapConfig{Enabled: false})

	if s.Enabled() {
		t.Error("重载后应禁用")
	}
	if rec := doRPC(s, "test-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`); rec.Code != 503 {
		t.Errorf("禁用后应 503，实际 %d", rec.Code)
	}
}

func TestMcpReloadPreservesConfirmGateAndLimiter(t *testing.T) {
	s, reg := newTestServer(t, nil)

	// 造一个确认门等待项（高危工具 FileDelete 已授权）
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"FileDelete","arguments":{"path":"x.txt"}}}`
	if rec := doRPC(s, "test-token", body); !strings.Contains(rec.Body.String(), "waiting_confirm") {
		t.Fatalf("应返回 waiting_confirm，实际 %s", rec.Body.String())
	}

	// 热重载不应清空确认门（进行中的高危确认不被重载打断）
	s.Reload(&BootstrapConfig{
		Enabled:     true,
		PathPrefix:  "/test-mcp",
		IPAllowlist: []string{"*"},
		Credentials: []*Credential{
			{ID: "key_1", Token: "test-token", ToolIDs: reg.LowRiskIDs(), Active: true,
				CreatedAt: time.Now().Format("2006-01-02 15:04:05"), ExpiresDays: 30},
		},
	})

	// 用相同参数 + confirm=true 应命中既有等待项进入已确认执行路径
	// （若确认门被清空，此处会重新返回 waiting_confirm）
	confirmBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"FileDelete","arguments":{"path":"x.txt","confirm":true}}}`
	rec := doRPC(s, "test-token", confirmBody)
	if strings.Contains(rec.Body.String(), "waiting_confirm") {
		t.Error("确认门应跨热重载保留，不应重新进入等待")
	}
}
