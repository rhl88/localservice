// internal/task/types.go
package task

import "context"

// TaskRequest 任务请求
type TaskRequest struct {
	AppID  string                 `json:"app_id"`
	Type   string                 `json:"type"`
	Mode   string                 `json:"mode"` // sync | async
	Params map[string]interface{} `json:"params"`
}

// TaskResult 任务执行结果
type TaskResult struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data"`
	Error   string      `json:"error,omitempty"`
}

// TaskHandler 任务处理器接口
type TaskHandler interface {
	Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error)
}

// registry 任务处理器注册表
var registry = map[string]TaskHandler{}

// Register 注册任务处理器
func Register(taskType string, handler TaskHandler) {
	registry[taskType] = handler
}

// GetHandler 获取任务处理器
func GetHandler(taskType string) (TaskHandler, bool) {
	h, ok := registry[taskType]
	return h, ok
}
