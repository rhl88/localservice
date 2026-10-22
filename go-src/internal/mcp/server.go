// internal/mcp/server.go
//
// MCP 协议服务（Streamable HTTP 传输，MCP 2025-03-26）。
//
// 自研轻量实现，不引入外部 MCP SDK——与本地服务既有 llmrelay（自研透传）
// 保持同一技术取向，且便于严格控制鉴权、限流、审计与确认门的介入点。
//
// 端点：POST {pathPrefix}/mcp，单端点承载全部 JSON-RPC 请求
//   - initialize            握手，返回 serverInfo 与 capabilities
//   - notifications/initialized  握手完成通知（无响应体，返回 202）
//   - tools/list            工具发现，按凭据权限过滤
//   - tools/call            工具调用，经权限校验与确认门后执行
//
// 鉴权：Authorization: Bearer <凭据 Token>；先过 IP 白名单，再过限流，最后校验凭据。
package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ProtocolVersion 支持的 MCP 协议版本
const ProtocolVersion = "2025-03-26"

// ServerInfo 服务标识（握手返回）
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Config MCP 服务运行配置（由本地服务配置下发）
type Config struct {
	Enabled         bool          `json:"enabled"`
	PathPrefix      string        `json:"path_prefix"`       // 随机路由前缀，如 /mcp-xxxx
	IPAllowlist     []string      `json:"ip_allowlist"`      // 空=拦截所有；["*"]=放行全部
	RateLimitMax    int           `json:"rate_limit_max"`    // 每窗口请求上限
	RateLimitWindow int           `json:"rate_limit_window"` // 窗口秒数
	Credentials     []*Credential `json:"credentials"`
	DefaultTimeout  int           `json:"default_timeout"` // 工具默认超时（秒）
	MaxConcurrent   int           `json:"max_concurrent"`  // 并发工具数上限
}

// DefaultConfig 默认配置（未启用）
func DefaultConfig() *Config {
	return &Config{
		Enabled:         false,
		PathPrefix:      "",
		IPAllowlist:     []string{"*"},
		RateLimitMax:    300,
		RateLimitWindow: 60,
		DefaultTimeout:  40,
		MaxConcurrent:   12,
	}
}

// Normalize 规整配置（填充默认值、规范化路径前缀与凭据授权集）
func (c *Config) Normalize(reg *Registry) {
	if c.RateLimitMax <= 0 {
		c.RateLimitMax = 300
	}
	if c.RateLimitWindow <= 0 {
		c.RateLimitWindow = 60
	}
	if c.DefaultTimeout <= 0 {
		c.DefaultTimeout = 40
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 12
	}
	if c.PathPrefix == "" {
		c.PathPrefix = "/mcp"
	}
	if !strings.HasPrefix(c.PathPrefix, "/") {
		c.PathPrefix = "/" + c.PathPrefix
	}
	c.PathPrefix = strings.TrimRight(c.PathPrefix, "/")

	registered := reg.IDs()
	for _, cred := range c.Credentials {
		if cred == nil {
			continue
		}
		cred.ToolIDs = NormalizeToolIDs(cred.ToolIDs, registered)
		cred.Disabled = NormalizeToolIDs(cred.Disabled, registered)
	}
}

// serverSnapshot 不可变运行快照（热重载时整体原子替换，读侧无锁）
type serverSnapshot struct {
	config    *Config
	store     *CredentialStore
	allowlist *IPAllowlist
}

// Server MCP 协议服务
type Server struct {
	registry *Registry
	snap     atomic.Pointer[serverSnapshot]
	limiter  *RateLimiter
	confirm  *ConfirmGate
	info     ServerInfo
	audit    AuditSink
	now      func() time.Time
}

// AuditSink 审计日志接收端（由本地服务 storage 实现；可空）
type AuditSink interface {
	Log(entry *AuditEntry)
}

// AuditEntry 审计记录
type AuditEntry struct {
	Time       string  `json:"time"`
	IP         string  `json:"ip"`
	Auth       string  `json:"auth"` // 凭据 ID（不含 Token）
	Tool       string  `json:"tool"`
	Result     string  `json:"result"` // ok / error / denied / waiting
	Msg        string  `json:"msg"`
	DurationMs float64 `json:"duration_ms"`
}

// NewServer 创建 MCP 服务
func NewServer(reg *Registry, cfg *Config, version string, audit AuditSink) *Server {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	cfg.Normalize(reg)

	s := &Server{
		registry: reg,
		limiter:  NewRateLimiter(cfg.RateLimitMax, cfg.RateLimitWindow),
		confirm:  NewConfirmGate(ConfirmTTL * time.Second),
		info: ServerInfo{
			Name:    "cmspro-local-service",
			Version: version,
		},
		audit: audit,
		now:   time.Now,
	}
	s.snap.Store(&serverSnapshot{
		config:    cfg,
		store:     NewCredentialStore(cfg.Credentials),
		allowlist: NewIPAllowlist(cfg.IPAllowlist),
	})
	return s
}

// snapshot 当前运行快照
func (s *Server) snapshot() *serverSnapshot { return s.snap.Load() }

// Enabled 是否启用
func (s *Server) Enabled() bool { return s.snapshot().config.Enabled }

// PathPrefix 路由前缀
func (s *Server) PathPrefix() string { return s.snapshot().config.PathPrefix }

// MCPPath 完整 MCP 端点路径
func (s *Server) MCPPath() string { return s.snapshot().config.PathPrefix + "/mcp" }

// Config 当前配置（只读，供本地管理接口展示）
func (s *Server) Config() *Config { return s.snapshot().config }

// CredentialCount 有效凭据数（含停用与过期，供管理端点展示）
func (s *Server) CredentialCount() int { return s.snapshot().store.Count() }

// ServeHTTP 处理 MCP 端点请求
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := s.now()
	snap := s.snapshot()

	if !snap.config.Enabled {
		writeRPCError(w, http.StatusServiceUnavailable, nil, -32000, "MCP 服务未启用")
		return
	}
	if r.Method != http.MethodPost {
		// Streamable HTTP 下 GET 用于服务端推送，本实现不支持
		writeRPCError(w, http.StatusMethodNotAllowed, nil, -32600, "仅支持 POST 请求")
		return
	}

	clientIP := extractClientIP(r)

	// 1. IP 白名单
	if !snap.allowlist.IsAllowed(clientIP) {
		s.auditLog(clientIP, "", "-", "denied", "ip not allowed", start)
		writeRPCError(w, http.StatusForbidden, nil, -32000, "IP 不在白名单内")
		return
	}

	// 2. 凭据鉴权
	token := extractBearer(r)
	cred := snap.store.FindByToken(token)
	if cred == nil || !cred.Active || cred.IsExpired(s.now()) {
		s.auditLog(clientIP, "", "-", "denied", "unauthorized", start)
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeRPCError(w, http.StatusUnauthorized, nil, -32000, "凭据无效或已过期")
		return
	}

	// 3. 限流（按凭据 + IP）
	if !s.limiter.Allow(cred.ID + ":" + clientIP) {
		s.auditLog(clientIP, cred.ID, "-", "denied", "rate limited", start)
		writeRPCError(w, http.StatusTooManyRequests, nil, -32000, "请求过于频繁，请稍后重试")
		return
	}

	// 4. 解析 JSON-RPC 请求
	body, err := io.ReadAll(io.LimitReader(r.Body, 4*1024*1024))
	if err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, -32700, "读取请求体失败")
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, http.StatusBadRequest, nil, -32700, "请求体不是合法 JSON")
		return
	}

	// 5. 分发
	switch req.Method {
	case "initialize":
		s.handleInitialize(w, &req, clientIP, cred.ID, start)
	case "notifications/initialized", "notifications/cancelled":
		// 通知类无响应体
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		writeRPCResult(w, req.ID, map[string]interface{}{})
	case "tools/list":
		s.handleListTools(w, &req, clientIP, cred, start)
	case "tools/call":
		s.handleCallTool(w, &req, clientIP, cred, start)
	default:
		s.auditLog(clientIP, cred.ID, req.Method, "error", "method not found", start)
		writeRPCError(w, http.StatusOK, req.ID, -32601, "不支持的方法: "+req.Method)
	}
}

// handleInitialize 握手
func (s *Server) handleInitialize(w http.ResponseWriter, req *rpcRequest, ip, credID string, start time.Time) {
	writeRPCResult(w, req.ID, map[string]interface{}{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo":   s.info,
		"instructions": Instructions,
	})
	s.auditLog(ip, credID, "initialize", "ok", "", start)
}

// handleListTools 工具发现（按凭据授权集过滤）
func (s *Server) handleListTools(w http.ResponseWriter, req *rpcRequest, ip string, cred *Credential, start time.Time) {
	granted := cred.EffectiveTools(s.registry.IDs())

	tools := make([]map[string]interface{}, 0)
	for _, tool := range s.registry.List() {
		if !granted[tool.ToolID] {
			continue
		}
		schema := tool.InputSchema
		if tool.Confirm {
			schema = withConfirmParam(schema)
		}
		tools = append(tools, map[string]interface{}{
			"name":        tool.ToolID,
			"description": tool.Description,
			"inputSchema": schema,
		})
	}

	writeRPCResult(w, req.ID, map[string]interface{}{"tools": tools})
	s.auditLog(ip, cred.ID, "tools/list", "ok", fmt.Sprintf("tools=%d", len(tools)), start)
}

// handleCallTool 工具调用
func (s *Server) handleCallTool(w http.ResponseWriter, req *rpcRequest, ip string, cred *Credential, start time.Time) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPCError(w, http.StatusOK, req.ID, -32602, "参数格式错误")
		return
	}

	tool, ok := s.registry.Get(params.Name)
	if !ok {
		writeRPCResult(w, req.ID, toolErrorContent("工具不存在: "+params.Name))
		s.auditLog(ip, cred.ID, params.Name, "error", "tool not found", start)
		return
	}

	// 权限校验：凭据授权集
	granted := cred.EffectiveTools(s.registry.IDs())
	if !granted[tool.ToolID] {
		writeRPCResult(w, req.ID, toolErrorContent("该凭据未授权调用工具: "+tool.ToolID))
		s.auditLog(ip, cred.ID, tool.ToolID, "denied", "tool not authorized", start)
		return
	}

	args := params.Arguments
	if args == nil {
		args = map[string]interface{}{}
	}

	// 二次确认门：高风险工具首次调用只建立确认记录
	confirmFlag := ArgBool(args, "confirm")
	delete(args, "confirm")
	if tool.Confirm {
		state, msg := s.confirm.Check(cred.ID, tool.ToolID, args, confirmFlag)
		switch state {
		case "waiting":
			writeRPCResult(w, req.ID, toolErrorThenData(map[string]interface{}{
				"status":      true,
				"msg":         "等待用户确认",
				"state":       "waiting_confirm",
				"executed":    false,
				"instruction": ConfirmInstruction,
			}))
			s.auditLog(ip, cred.ID, tool.ToolID, "waiting", "waiting_confirm", start)
			return
		case "rejected":
			writeRPCResult(w, req.ID, toolErrorContent(msg))
			s.auditLog(ip, cred.ID, tool.ToolID, "denied", msg, start)
			return
		}
	}

	// 执行
	result := FitResult(ExecuteTask(tool, args))
	writeRPCResult(w, req.ID, toolResultContent(result))

	auditResult := "ok"
	if !result.Status {
		auditResult = "error"
	}
	s.auditLog(ip, cred.ID, tool.ToolID, auditResult, result.Msg, start)
}

// auditLog 写入审计（sink 为空时静默跳过）
func (s *Server) auditLog(ip, auth, tool, result, msg string, start time.Time) {
	if s.audit == nil {
		return
	}
	s.audit.Log(&AuditEntry{
		Time:       s.now().Format("2006-01-02 15:04:05"),
		IP:         ip,
		Auth:       auth,
		Tool:       tool,
		Result:     result,
		Msg:        msg,
		DurationMs: float64(s.now().Sub(start).Milliseconds()),
	})
}

// ============ JSON-RPC 与响应辅助 ============

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	if id == nil {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	if id == nil {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	})
}

// toolResultContent 工具成功/失败结果包装为 MCP content 结构
func toolResultContent(res *Result) map[string]interface{} {
	payload, err := json.Marshal(res)
	if err != nil {
		payload = []byte(`{"status":false,"msg":"结果序列化失败"}`)
	}
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": string(payload)},
		},
	}
}

// toolErrorContent 工具级错误（协议层正常，业务层失败）——按 MCP 规范用 isError 标记
func toolErrorContent(msg string) map[string]interface{} {
	payload, _ := json.Marshal(map[string]interface{}{"status": false, "msg": msg})
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": string(payload)},
		},
		"isError": true,
	}
}

// toolErrorThenData 非错误但需按 content 传递的结构（如等待确认）
func toolErrorThenData(data map[string]interface{}) map[string]interface{} {
	payload, _ := json.Marshal(data)
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": string(payload)},
		},
	}
}

// withConfirmParam 为需确认的工具注入 confirm 参数
func withConfirmParam(schema map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(schema))
	for k, v := range schema {
		out[k] = v
	}
	props, _ := out["properties"].(map[string]interface{})
	newProps := make(map[string]interface{}, len(props)+1)
	for k, v := range props {
		newProps[k] = v
	}
	newProps["confirm"] = map[string]interface{}{
		"type":        "boolean",
		"default":     false,
		"description": ConfirmDescription,
	}
	out["properties"] = newProps
	return out
}

// extractBearer 从 Authorization 头提取 Token
func extractBearer(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

// extractClientIP 提取客户端 IP（仅回环对端才信任 X-Real-IP，防伪造）
func extractClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "127.0.0.1" || host == "::1" {
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}
	return host
}
