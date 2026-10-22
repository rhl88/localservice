// internal/mcp/bootstrap.go
//
// MCP 服务装配辅助：把本地服务配置文件中的 MCP 配置转换为运行时配置，
// 并提供审计落库适配。集中在此避免 main 与 config 包反向依赖 mcp 包。
package mcp

import (
	"log"
)

// BootstrapConfig 构造 MCP 配置的输入（由调用方从 config.Config 映射后传入，
// 避免本包依赖 config 包）
type BootstrapConfig struct {
	Enabled         bool
	PathPrefix      string
	IPAllowlist     []string
	RateLimitMax    int
	RateLimitWindow int
	DefaultTimeout  int
	MaxConcurrent   int
	Credentials     []*Credential
}

// NewServerFrom 从引导配置创建 MCP 服务并注册内置工具。
//
// registry 为 nil 时使用全局注册表；工具注册失败不阻断启动（记日志继续），
// 避免单个工具定义问题导致整个本地服务无法启动。
func NewServerFrom(bc *BootstrapConfig, version string, audit AuditSink, registry *Registry) *Server {
	reg := registry
	if reg == nil {
		reg = DefaultRegistry()
	}

	// 注册内置工具（幂等：已注册的工具会被跳过而非报错）
	if err := registerBuiltinToolsIdempotent(reg); err != nil {
		log.Printf("[mcp] 内置工具注册异常: %v", err)
	}

	cfg := bootstrapConfigToConfig(bc, reg)
	srv := NewServer(reg, cfg, version, audit)

	if cfg.Enabled {
		log.Printf("[mcp] MCP 服务已启用，端点 %s（工具 %d 个，凭据 %d 个）",
			srv.MCPPath(), reg.Count(), srv.CredentialCount())
	} else {
		log.Printf("[mcp] MCP 服务未启用")
	}
	return srv
}

// Reload 热更新运行配置（PHP 后台变更下发后由 /api/mcp/reload 端点触发）。
//
// 原子替换配置、凭据与白名单——进行中的请求继续使用旧快照直至完成，
// 新请求立即使用新配置。限流计数与确认门保持不变：前者避免重载被利用
// 绕过限流，后者不中断进行中的高危操作确认流程。
func (s *Server) Reload(bc *BootstrapConfig) {
	reg := s.registry
	if reg == nil {
		reg = DefaultRegistry()
	}
	if err := registerBuiltinToolsIdempotent(reg); err != nil {
		log.Printf("[mcp] 热重载时内置工具注册异常: %v", err)
	}

	cfg := bootstrapConfigToConfig(bc, reg)
	s.snap.Store(&serverSnapshot{
		config:    cfg,
		store:     NewCredentialStore(cfg.Credentials),
		allowlist: NewIPAllowlist(cfg.IPAllowlist),
	})

	if cfg.Enabled {
		log.Printf("[mcp] 配置已热重载，端点 %s（凭据 %d 个）", s.MCPPath(), s.CredentialCount())
	} else {
		log.Printf("[mcp] 配置已热重载，MCP 服务未启用")
	}
}

// bootstrapConfigToConfig 把引导配置转换为运行配置（NewServerFrom 与 Reload 共用）
func bootstrapConfigToConfig(bc *BootstrapConfig, reg *Registry) *Config {
	cfg := DefaultConfig()
	if bc != nil {
		cfg.Enabled = bc.Enabled
		if bc.PathPrefix != "" {
			cfg.PathPrefix = bc.PathPrefix
		}
		if bc.IPAllowlist != nil {
			cfg.IPAllowlist = bc.IPAllowlist
		}
		cfg.RateLimitMax = bc.RateLimitMax
		cfg.RateLimitWindow = bc.RateLimitWindow
		cfg.DefaultTimeout = bc.DefaultTimeout
		cfg.MaxConcurrent = bc.MaxConcurrent
		cfg.Credentials = bc.Credentials
	}
	cfg.Normalize(reg)
	return cfg
}

// registerBuiltinToolsIdempotent 注册内置工具，已存在的跳过
func registerBuiltinToolsIdempotent(reg *Registry) error {
	for _, tool := range builtinTools() {
		if _, exists := reg.Get(tool.ToolID); exists {
			continue
		}
		if err := reg.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

// StorageAuditSink 把审计记录写入本地服务 SQLite（storage.DB 的 InsertLog）。
//
// 通过函数注入避免 mcp 包直接依赖 storage 包（保持协议层与存储层解耦）。
type StorageAuditSink struct {
	insert func(level, message string) error
}

// NewStorageAuditSink 创建审计落库适配器（insert 为空时降级为仅日志输出）
func NewStorageAuditSink(insert func(level, message string) error) *StorageAuditSink {
	return &StorageAuditSink{insert: insert}
}

// Log 实现 AuditSink
func (s *StorageAuditSink) Log(entry *AuditEntry) {
	if entry == nil {
		return
	}
	msg := "MCP " + entry.Tool + " [" + entry.Result + "] ip=" + entry.IP
	if entry.Auth != "" {
		msg += " auth=" + entry.Auth
	}
	if entry.Msg != "" {
		msg += " msg=" + entry.Msg
	}

	level := "info"
	if entry.Result == "error" || entry.Result == "denied" {
		level = "warn"
	}

	if s.insert == nil {
		log.Printf("[mcp][audit] %s", msg)
		return
	}
	if err := s.insert(level, msg); err != nil {
		log.Printf("[mcp] 审计写入失败: %v", err)
	}
}
