package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"cmspro-local-service/internal/config"
	"cmspro-local-service/internal/llmrelay"
	"cmspro-local-service/internal/mcp"
)

// Server HTTP 服务器封装
type Server struct {
	httpServer *http.Server
	handler    *Handlers
	mcp        *mcp.Server
}

// NewServer 创建 HTTP 服务器并注册路由
func NewServer(handlers *Handlers) *Server {
	mux := http.NewServeMux()

	// 内部 API 路由（Bearer Token 认证）
	mux.HandleFunc("/api/status", handlers.Status)
	mux.HandleFunc("/api/task", handlers.CreateTask)
	mux.HandleFunc("/api/tasks", handlers.ListTasks)
	mux.HandleFunc("/api/logs", handlers.ListLogs)
	mux.HandleFunc("/api/logs/clear", handlers.ClearLogs)
	mux.HandleFunc("/api/tasks/clear", handlers.ClearTasks)
	mux.HandleFunc("/api/extensions", handlers.ListExtensions)
	mux.HandleFunc("/api/task/", handlers.TaskDetail)
	mux.HandleFunc("/api/extensions/", handlers.ExtensionDetail)
	// MCP 工具清单与配置查询（后台界面用，内部认证）
	mux.HandleFunc("/api/mcp/tools", handlers.McpTools)
	mux.HandleFunc("/api/mcp/config", handlers.McpConfig)
	// MCP 配置热重载（PHP 后台配置下发后触发，内部认证）
	mux.HandleFunc("/api/mcp/reload", handlers.McpReload)

	// 开放 API 路由（X-API-Key 认证）
	// 供第三方跨服务器调用，仅暴露任务执行能力
	openMux := http.NewServeMux()
	openMux.HandleFunc("/api/open/task", handlers.CreateTask)
	openMux.HandleFunc("/api/open/tasks", handlers.ListTasks)
	openMux.HandleFunc("/api/open/task/", handlers.TaskDetail)
	openMux.HandleFunc("/api/open/status", handlers.Status)
	openAPIHandler := OpenAPIAuthMiddleware(openMux)

	// MCP 服务：工具注册表 + 协议服务（配置由本地服务配置文件下发）
	registry := mcp.DefaultRegistry()
	if handlers.mcp == nil {
		if err := mcp.RegisterBuiltinTools(registry); err != nil {
			log.Printf("[mcp] 内置工具注册失败: %v", err)
		}
	}
	mcpServer := handlers.mcp

	// 合并路由：开放 API 路径走独立中间件，其余走内部认证
	// AI 透传路由（/open/llm/）不做应用级鉴权：鉴权委托各应用决策接口，
	// 客户端 Authorization 原样透传；服务仅监听 127.0.0.1
	// MCP 路由（{prefix}/mcp）自带鉴权/白名单/限流/审计，不走内部认证
	combinedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/open/llm/") {
			llmrelay.Handler(w, r)
			return
		}
		if mcpServer != nil && mcpServer.Enabled() && r.URL.Path == mcpServer.MCPPath() {
			mcpServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/open/") {
			openAPIHandler.ServeHTTP(w, r)
			return
		}
		AuthMiddleware(mux).ServeHTTP(w, r)
	})

	cfg := config.Get()
	addr := fmt.Sprintf("127.0.0.1:%s", cfg.Port)

	return &Server{
		httpServer: &http.Server{
			Addr:    addr,
			Handler: combinedHandler,
		},
		handler: handlers,
		mcp:     mcpServer,
	}
}

// Start 启动 HTTP 服务器监听
func (s *Server) Start() error {
	log.Printf("服务监听: http://%s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// writeJSON 输出 JSON 响应
func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// writeSuccess 输出统一成功响应
func writeSuccess(w http.ResponseWriter, data interface{}) {
	writeJSON(w, map[string]interface{}{
		"code":    0,
		"message": "success",
		"data":    data,
	})
}
