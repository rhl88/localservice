// internal/mcp/registry.go
//
// MCP 工具注册表：工具元数据、风险分级与调用分发。
//
// 设计对齐宝塔 MCP 的 register_tool 模式：工具以 Go 结构体声明
// （ToolID/NameCN/Description/InputSchema/RiskLevel/Confirm/Category），
// 注册到全局注册表；执行时由 Registry 统一做参数校验、权限校验与确认门。
package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// 风险等级：low 默认授权，medium/high 需在后台凭据中显式勾选
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// 工具结果字节上限：超过则截断并提示缩小范围，避免撑爆客户端上下文
const MaxResultBytes = 50 * 1024

// ToolHandler 工具执行入口：入参为 JSON 解码后的对象，返回统一结果结构
type ToolHandler func(args map[string]interface{}) *Result

// Result 工具统一返回结构（与本地服务既有任务结果语义一致）
type Result struct {
	Status bool                   `json:"status"`
	Msg    string                 `json:"msg"`
	Data   interface{}            `json:"data,omitempty"`
	State  string                 `json:"state,omitempty"`
	Extra  map[string]interface{} `json:"-"`
}

// Tool 工具定义
type Tool struct {
	ToolID      string                 // PascalCase 唯一标识（如 SystemInfo）
	TaskType    string                 // 映射的本地服务任务类型（如 system.info）
	NameCN      string                 // 中文名（后台展示）
	Description string                 // 描述（注入 MCP 工具说明，指导 AI 使用）
	InputSchema map[string]interface{} // JSON Schema 参数定义
	RiskLevel   string                 // low / medium / high
	Category    string                 // 分类（后台按此折叠展示）
	Confirm     bool                   // 是否需二次确认
	BasicTool   bool                   // 基础工具（仅在不具备本机能力的客户端下提供）
	Timeout     int                    // 覆盖默认超时（秒），0 用默认
}

// Registry 工具注册表
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
	order []string // 保持注册顺序，供列表稳定展示
}

var (
	globalRegistry = &Registry{tools: make(map[string]*Tool)}
)

// DefaultRegistry 返回全局注册表
func DefaultRegistry() *Registry { return globalRegistry }

// Register 注册工具（tool_id 重复报错）
func (r *Registry) Register(t *Tool) error {
	if t == nil {
		return fmt.Errorf("工具不能为空")
	}
	if t.ToolID == "" {
		return fmt.Errorf("tool_id 不能为空")
	}
	if t.TaskType == "" {
		return fmt.Errorf("工具 %s 缺少 task_type", t.ToolID)
	}
	switch t.RiskLevel {
	case RiskLow, RiskMedium, RiskHigh:
	default:
		return fmt.Errorf("工具 %s 的 risk_level 非法: %s", t.ToolID, t.RiskLevel)
	}
	if t.Timeout < 0 || t.Timeout > 300 {
		return fmt.Errorf("工具 %s 的 timeout 须在 0-300 秒", t.ToolID)
	}
	if t.InputSchema == nil {
		t.InputSchema = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.ToolID]; exists {
		return fmt.Errorf("tool_id 已注册: %s", t.ToolID)
	}
	r.tools[t.ToolID] = t
	r.order = append(r.order, t.ToolID)
	return nil
}

// MustRegister 注册失败即 panic（供 init 阶段硬编码注册使用）
func (r *Registry) MustRegister(t *Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Get 取工具
func (r *Registry) Get(toolID string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[toolID]
	return t, ok
}

// List 按注册顺序返回全部工具
func (r *Registry) List() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Tool, 0, len(r.order))
	for _, id := range r.order {
		if t, ok := r.tools[id]; ok {
			out = append(out, t)
		}
	}
	return out
}

// IDs 返回全部工具标识
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Count 工具总数
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// LowRiskIDs 低风险工具标识（凭据默认授权集）
func (r *Registry) LowRiskIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0)
	for _, id := range r.order {
		if t, ok := r.tools[id]; ok && t.RiskLevel == RiskLow {
			out = append(out, id)
		}
	}
	return out
}

// RiskCounts 各风险等级工具数（后台展示「已授权 N/M」用）
func (r *Registry) RiskCounts() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	counts := map[string]int{RiskLow: 0, RiskMedium: 0, RiskHigh: 0}
	for _, t := range r.tools {
		counts[t.RiskLevel]++
	}
	return counts
}

// Meta 工具元数据（后台界面与配置下发用）
type Meta struct {
	ToolID      string                 `json:"tool_id"`
	Name        string                 `json:"name"`
	TaskType    string                 `json:"task_type"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
	RiskLevel   string                 `json:"risk_level"`
	Category    string                 `json:"category"`
	Confirm     bool                   `json:"confirm"`
	BasicTool   bool                   `json:"basic_tool"`
}

// MetaList 全部工具的元数据列表（分类内按 tool_id 稳定排序）
func (r *Registry) MetaList() []Meta {
	tools := r.List()
	out := make([]Meta, 0, len(tools))
	for _, t := range tools {
		name := t.NameCN
		if name == "" {
			name = t.ToolID
		}
		category := t.Category
		if category == "" {
			category = "其他"
		}
		out = append(out, Meta{
			ToolID:      t.ToolID,
			Name:        name,
			TaskType:    t.TaskType,
			Description: t.Description,
			InputSchema: t.InputSchema,
			RiskLevel:   t.RiskLevel,
			Category:    category,
			Confirm:     t.Confirm,
			BasicTool:   t.BasicTool,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].ToolID < out[j].ToolID
	})
	return out
}

// FitResult 限制结果体积（超限返回失败提示，避免序列化后撑爆上下文）
func FitResult(res *Result) *Result {
	if res == nil {
		return &Result{Status: false, Msg: "工具未返回结果"}
	}
	payload, err := json.Marshal(res)
	if err != nil {
		return &Result{Status: false, Msg: "工具返回结构无法序列化"}
	}
	if len(payload) <= MaxResultBytes {
		return res
	}
	return &Result{
		Status: false,
		Msg:    fmt.Sprintf("工具结果超过 %dKB 限制，请缩小查询范围后重试", MaxResultBytes/1024),
	}
}

// schema 构造辅助：简化工具定义书写

// ObjSchema 构造对象型 input_schema
func ObjSchema(props map[string]interface{}, required ...string) map[string]interface{} {
	if props == nil {
		props = map[string]interface{}{}
	}
	schema := map[string]interface{}{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// Str 字符串参数定义
func Str(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}

// StrEnum 枚举字符串参数
func StrEnum(desc string, values ...string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"description": desc,
		"enum":        values,
	}
}

// Int 整数参数定义
func Int(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "integer", "description": desc}
}

// Bool 布尔参数定义
func Bool(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "boolean", "description": desc}
}

// ArgString 从入参取字符串
func ArgString(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ArgInt 从入参取整数（兼容 JSON number/float64/字符串数字）
func ArgInt(args map[string]interface{}, key string, def int) int {
	if args == nil {
		return def
	}
	v, ok := args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
	case string:
		var i int
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &i); err == nil {
			return i
		}
	}
	return def
}

// ArgBool 从入参取布尔
func ArgBool(args map[string]interface{}, key string) bool {
	if args == nil {
		return false
	}
	v, ok := args[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// ArgMap 从入参取对象
func ArgMap(args map[string]interface{}, key string) map[string]interface{} {
	if args == nil {
		return nil
	}
	if v, ok := args[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// Ok 成功结果
func Ok(msg string, data interface{}) *Result {
	return &Result{Status: true, Msg: msg, Data: data}
}

// Fail 失败结果
func Fail(msg string) *Result {
	return &Result{Status: false, Msg: msg}
}
