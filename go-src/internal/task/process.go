// internal/task/process.go
package task

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type ProcessListHandler struct{}

func (h *ProcessListHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	filter, _ := params["filter"].(string)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH")
	} else {
		cmd = exec.CommandContext(ctx, "ps", "aux")
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("获取进程列表失败: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	var processes []map[string]interface{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(filter)) {
			continue
		}
		processes = append(processes, map[string]interface{}{
			"raw": line,
		})
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"processes": processes, "count": len(processes)},
	}, nil
}

type ProcessStartHandler struct{}

func (h *ProcessStartHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	executable, _ := params["executable"].(string)
	if executable == "" {
		return nil, fmt.Errorf("缺少 executable 参数")
	}

	argsRaw, _ := params["args"].([]interface{})
	var args []string
	for _, a := range argsRaw {
		args = append(args, fmt.Sprintf("%v", a))
	}

	cmd := exec.Command(executable, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动进程失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data: map[string]interface{}{
			"pid": cmd.Process.Pid,
		},
	}, nil
}

type ProcessKillHandler struct{}

func (h *ProcessKillHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	pidFloat, ok := params["pid"].(float64)
	if !ok {
		return nil, fmt.Errorf("缺少 pid 参数")
	}
	pid := int(pidFloat)

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("查找进程失败: %w", err)
	}

	if err := proc.Kill(); err != nil {
		return nil, fmt.Errorf("终止进程失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"killed": true, "pid": pid},
	}, nil
}

func init() {
	Register("process.list", &ProcessListHandler{})
	Register("process.start", &ProcessStartHandler{})
	Register("process.kill", &ProcessKillHandler{})
	Register("process.kill_by_name", &ProcessKillByNameHandler{})
}

// ProcessKillByNameHandler 按进程名终止进程
type ProcessKillByNameHandler struct{}

func (h *ProcessKillByNameHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("缺少 name 参数")
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "taskkill", "/F", "/IM", name)
	} else {
		cmd = exec.CommandContext(ctx, "pkill", "-f", name)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		// pkill/taskkill 未匹配到进程时返回非零退出码，视为无进程可终止而非错误
		if _, ok := err.(*exec.ExitError); ok {
			return &TaskResult{
				Success: true,
				Data:    map[string]interface{}{"killed": false, "name": name, "output": string(output)},
			}, nil
		}
		return nil, fmt.Errorf("终止进程失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"killed": true, "name": name, "output": string(output)},
	}, nil
}
