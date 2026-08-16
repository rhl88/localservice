// internal/task/service.go
package task

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ensureWindows 确保当前系统为 Windows，服务管理任务仅支持 Windows
func ensureWindows() error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("服务管理任务仅支持 Windows 平台")
	}
	return nil
}

// ServiceListHandler 列出系统服务（可按 filter 过滤名称）
type ServiceListHandler struct{}

func (h *ServiceListHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	if err := ensureWindows(); err != nil {
		return nil, err
	}
	filter, _ := params["filter"].(string)

	cmd := exec.CommandContext(ctx, "sc", "query", "type=", "service", "state=", "all")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("查询服务列表失败: %w", err)
	}

	var services []map[string]interface{}
	var current map[string]interface{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SERVICE_NAME:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "SERVICE_NAME:"))
			if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
				current = nil
				continue
			}
			current = map[string]interface{}{"name": name}
			services = append(services, current)
		} else if strings.HasPrefix(line, "STATE") && current != nil {
			// 形如 STATE : 4  RUNNING
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				current["state"] = parts[len(parts)-1]
			}
		}
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"services": services, "count": len(services)},
	}, nil
}

// ServiceStatusHandler 查询指定服务状态
type ServiceStatusHandler struct{}

func (h *ServiceStatusHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	if err := ensureWindows(); err != nil {
		return nil, err
	}
	name, _ := params["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("缺少 name 参数")
	}

	cmd := exec.CommandContext(ctx, "sc", "query", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("查询服务状态失败: %w", err)
	}

	state := "UNKNOWN"
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "STATE") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				state = parts[len(parts)-1]
			}
		}
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"name": name, "state": state},
	}, nil
}

// ServiceStartHandler 启动指定服务
type ServiceStartHandler struct{}

func (h *ServiceStartHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	if err := ensureWindows(); err != nil {
		return nil, err
	}
	name, _ := params["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("缺少 name 参数")
	}

	cmd := exec.CommandContext(ctx, "sc", "start", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("启动服务失败: %s (%w)", string(output), err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"name": name, "started": true, "output": string(output)},
	}, nil
}

// ServiceStopHandler 停止指定服务
type ServiceStopHandler struct{}

func (h *ServiceStopHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	if err := ensureWindows(); err != nil {
		return nil, err
	}
	name, _ := params["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("缺少 name 参数")
	}

	cmd := exec.CommandContext(ctx, "sc", "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("停止服务失败: %s (%w)", string(output), err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"name": name, "stopped": true, "output": string(output)},
	}, nil
}

func init() {
	Register("service.list", &ServiceListHandler{})
	Register("service.status", &ServiceStatusHandler{})
	Register("service.start", &ServiceStartHandler{})
	Register("service.stop", &ServiceStopHandler{})
}
