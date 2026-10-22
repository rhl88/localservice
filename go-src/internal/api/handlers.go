package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"cmspro-local-service/internal/buildinfo"
	"cmspro-local-service/internal/extension"
	"cmspro-local-service/internal/mcp"
	"cmspro-local-service/internal/storage"
	"cmspro-local-service/internal/task"
)

// Handlers HTTP 请求处理器集合
type Handlers struct {
	db        *storage.DB
	tasks     *task.Manager
	extMgr    *extension.Manager
	mcp       *mcp.Server
	mcpReload func() error
}

// NewHandlers 创建 Handlers 实例
func NewHandlers(db *storage.DB, taskMgr *task.Manager, extMgr *extension.Manager) *Handlers {
	return &Handlers{db: db, tasks: taskMgr, extMgr: extMgr}
}

// SetMcp 注入 MCP 服务（启动时装配，供路由与配置查询端点使用）
func (h *Handlers) SetMcp(s *mcp.Server) {
	h.mcp = s
}

// SetMcpReloader 注入 MCP 配置热重载回调（由 main 装配：重读配置文件并 Reload）
func (h *Handlers) SetMcpReloader(fn func() error) {
	h.mcpReload = fn
}

// McpTools MCP 工具清单查询（后台界面用）
// 返回全部工具的元数据与各风险等级统计
func (h *Handlers) McpTools(w http.ResponseWriter, r *http.Request) {
	reg := mcp.DefaultRegistry()
	writeSuccess(w, map[string]interface{}{
		"tools":       reg.MetaList(),
		"total":       reg.Count(),
		"risk_counts": reg.RiskCounts(),
	})
}

// McpConfig MCP 服务运行配置查询（后台界面用，凭据 Token 已脱敏）
func (h *Handlers) McpConfig(w http.ResponseWriter, r *http.Request) {
	if h.mcp == nil {
		writeError(w, 503, "MCP 服务未装配")
		return
	}
	cfg := h.mcp.Config()
	writeSuccess(w, map[string]interface{}{
		"enabled":           cfg.Enabled,
		"path_prefix":       cfg.PathPrefix,
		"mcp_path":          h.mcp.MCPPath(),
		"ip_allowlist":      cfg.IPAllowlist,
		"rate_limit_max":    cfg.RateLimitMax,
		"rate_limit_window": cfg.RateLimitWindow,
		"default_timeout":   cfg.DefaultTimeout,
		"max_concurrent":    cfg.MaxConcurrent,
		"credential_count":  len(cfg.Credentials),
	})
}

// McpReload 触发 MCP 配置热重载（PHP 后台配置下发后调用，内部认证保护）
func (h *Handlers) McpReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "仅支持 POST 请求")
		return
	}
	if h.mcp == nil {
		writeError(w, 503, "MCP 服务未装配")
		return
	}
	if h.mcpReload == nil {
		writeError(w, 501, "热重载能力未装配，请重启本地服务使配置生效")
		return
	}
	if err := h.mcpReload(); err != nil {
		writeError(w, 500, "重载失败: "+err.Error())
		return
	}
	writeSuccess(w, map[string]interface{}{
		"reloaded":         true,
		"mcp_path":         h.mcp.MCPPath(),
		"enabled":          h.mcp.Enabled(),
		"credential_count": h.mcp.CredentialCount(),
	})
}

// Status 服务状态查询
func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	writeSuccess(w, map[string]interface{}{
		"version": buildinfo.Version,
		"status":  "running",
	})
}

// CreateTask 创建并执行任务
func (h *Handlers) CreateTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "方法不允许")
		return
	}

	var req task.TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "请求参数解析失败: "+err.Error())
		return
	}

	if req.AppID == "" {
		writeError(w, 400, "缺少 app_id 参数")
		return
	}
	if req.Type == "" {
		writeError(w, 400, "缺少 type 参数")
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = "sync"
	}

	if mode == "async" {
		taskID, err := h.tasks.ExecuteAsync(&req)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeSuccess(w, map[string]interface{}{"task_id": taskID})
	} else {
		result, t := h.tasks.ExecuteSync(&req)
		if t.Status == "failed" {
			errMsg := "任务执行失败"
			if t.Error != nil {
				errMsg = *t.Error
			}
			writeJSON(w, map[string]interface{}{
				"code":    500,
				"message": errMsg,
				"data":    result,
			})
			return
		}
		writeSuccess(w, result.Data)
	}
}

// TaskDetail 任务详情查询或取消
// 路径: /api/task/{id} 或 /api/task/{id}/cancel 或 /api/open/task/{id}
func (h *Handlers) TaskDetail(w http.ResponseWriter, r *http.Request) {
	// 兼容内部 API 和开放 API 两种路径前缀
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/open/task/") {
		path = path[len("/api/open/task/"):]
	} else {
		path = path[len("/api/task/"):]
	}
	if path == "" {
		writeError(w, 404, "任务不存在")
		return
	}

	parts := splitPath(path)
	if len(parts) == 0 {
		writeError(w, 404, "任务不存在")
		return
	}

	taskID := parts[0]

	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != "POST" {
			writeError(w, 405, "方法不允许")
			return
		}
		if h.tasks.Cancel(taskID) {
			writeSuccess(w, map[string]interface{}{"cancelled": true})
		} else {
			writeError(w, 404, "任务不存在或无法取消")
		}
		return
	}

	t, err := h.db.GetTask(taskID)
	if err != nil {
		writeError(w, 404, "任务不存在")
		return
	}
	writeSuccess(w, t)
}

// ListTasks 任务列表分页查询
func (h *Handlers) ListTasks(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 20
	}

	tasks, total, err := h.db.ListTasks(appID, status, page, pageSize)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeSuccess(w, map[string]interface{}{"items": tasks, "total": total, "page": page, "page_size": pageSize})
}

// ClearLogs 清空全部日志（调试用）
func (h *Handlers) ClearLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "方法不允许")
		return
	}
	if err := h.db.ClearLogs(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeSuccess(w, map[string]interface{}{"cleared": true})
}

// ClearTasks 清空全部任务记录（调试用）
func (h *Handlers) ClearTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "方法不允许")
		return
	}
	if err := h.db.ClearTasks(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeSuccess(w, map[string]interface{}{"cleared": true})
}

// ListLogs 日志列表分页查询
func (h *Handlers) ListLogs(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	level := r.URL.Query().Get("level")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = 50
	}

	logs, total, err := h.db.ListLogs(appID, level, page, pageSize)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeSuccess(w, map[string]interface{}{"items": logs, "total": total, "page": page, "page_size": pageSize})
}

// ListExtensions 拓展列表查询
func (h *Handlers) ListExtensions(w http.ResponseWriter, r *http.Request) {
	exts := h.extMgr.List()
	writeSuccess(w, exts)
}

// ExtensionDetail 拓展详情查询、测试或更新
// 路径: /api/extensions/{name} 或 /api/extensions/{name}/test
func (h *Handlers) ExtensionDetail(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/extensions/"):]
	if path == "" {
		writeError(w, 404, "拓展不存在")
		return
	}

	parts := splitPath(path)
	if len(parts) == 0 {
		writeError(w, 404, "拓展不存在")
		return
	}

	name := parts[0]

	if len(parts) == 2 && parts[1] == "test" {
		if r.Method != "POST" {
			writeError(w, 405, "方法不允许")
			return
		}
		if err := h.extMgr.Test(name); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeSuccess(w, map[string]interface{}{"available": true})
		return
	}

	if r.Method == "PUT" {
		var body struct {
			Path   string `json:"path"`
			Config string `json:"config"`
			Status int    `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "参数解析失败")
			return
		}
		h.extMgr.Update(name, body.Path, body.Config, body.Status)
		writeSuccess(w, map[string]interface{}{"updated": true})
		return
	}

	ext, ok := h.extMgr.Get(name)
	if !ok {
		writeError(w, 404, "拓展不存在")
		return
	}
	writeSuccess(w, ext)
}

// splitPath 按 "/" 分割路径，忽略空段
func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, c := range path {
		if c == '/' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
