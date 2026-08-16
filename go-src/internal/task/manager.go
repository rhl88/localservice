// internal/task/manager.go
package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"cmspro-local-service/internal/storage"
	"github.com/google/uuid"
)

type Manager struct {
	db        *storage.DB
	mu        sync.Mutex
	cancelMap map[string]context.CancelFunc
}

func NewManager(db *storage.DB) *Manager {
	return &Manager{
		db:        db,
		cancelMap: make(map[string]context.CancelFunc),
	}
}

// ExecuteSync 同步执行任务
func (m *Manager) ExecuteSync(req *TaskRequest) (*TaskResult, *storage.Task) {
	taskID := uuid.New().String()
	paramsJSON, _ := json.Marshal(req.Params)

	t := &storage.Task{
		ID:     taskID,
		AppID:  req.AppID,
		Type:   req.Type,
		Mode:   "sync",
		Params: string(paramsJSON),
		Status: "running",
	}
	m.db.CreateTask(t)
	m.db.UpdateTaskStatus(taskID, "running", nil, nil, nil)
	m.db.InsertLog(req.AppID, &taskID, "info", fmt.Sprintf("开始执行同步任务: %s", req.Type), nil)

	start := time.Now()
	result, err := m.execute(taskID, req)
	duration := time.Since(start).Milliseconds()

	if err != nil {
		errStr := err.Error()
		m.db.UpdateTaskStatus(taskID, "failed", nil, &errStr, &duration)
		m.db.InsertLog(req.AppID, &taskID, "error", fmt.Sprintf("任务失败: %s", err), nil)
		t.Status = "failed"
		t.Error = &errStr
	} else {
		resultJSON, _ := json.Marshal(result.Data)
		resultStr := string(resultJSON)
		m.db.UpdateTaskStatus(taskID, "success", &resultStr, nil, &duration)
		m.db.InsertLog(req.AppID, &taskID, "info", "任务执行成功", nil)
		t.Status = "success"
		t.Result = &resultStr
	}

	return result, t
}

// ExecuteAsync 异步执行任务，返回 task_id
func (m *Manager) ExecuteAsync(req *TaskRequest) (string, error) {
	taskID := uuid.New().String()
	paramsJSON, _ := json.Marshal(req.Params)

	t := &storage.Task{
		ID:     taskID,
		AppID:  req.AppID,
		Type:   req.Type,
		Mode:   "async",
		Params: string(paramsJSON),
		Status: "pending",
	}
	if err := m.db.CreateTask(t); err != nil {
		return "", fmt.Errorf("创建任务记录失败: %w", err)
	}

	go m.runAsync(taskID, req)

	return taskID, nil
}

func (m *Manager) runAsync(taskID string, req *TaskRequest) {
	// context 生命周期与 cancelMap 注册/清理统一由 execute 管理，此处不再重复创建
	m.db.UpdateTaskStatus(taskID, "running", nil, nil, nil)
	m.db.InsertLog(req.AppID, &taskID, "info", fmt.Sprintf("开始执行异步任务: %s", req.Type), nil)

	start := time.Now()
	result, err := m.execute(taskID, req)
	duration := time.Since(start).Milliseconds()

	if err != nil {
		errStr := err.Error()
		m.db.UpdateTaskStatus(taskID, "failed", nil, &errStr, &duration)
		m.db.InsertLog(req.AppID, &taskID, "error", fmt.Sprintf("任务失败: %s", err), nil)
	} else {
		resultJSON, _ := json.Marshal(result.Data)
		resultStr := string(resultJSON)
		m.db.UpdateTaskStatus(taskID, "success", &resultStr, nil, &duration)
		m.db.InsertLog(req.AppID, &taskID, "info", "任务执行成功", nil)
	}
}

func (m *Manager) execute(taskID string, req *TaskRequest) (*TaskResult, error) {
	handler, ok := GetHandler(req.Type)
	if !ok {
		return nil, fmt.Errorf("不支持的任务类型: %s", req.Type)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.mu.Lock()
	m.cancelMap[taskID] = cancel
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.cancelMap, taskID)
		m.mu.Unlock()
	}()

	result, err := handler.Execute(ctx, req.Params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Cancel 取消任务
func (m *Manager) Cancel(taskID string) bool {
	m.mu.Lock()
	cancel, ok := m.cancelMap[taskID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	m.db.UpdateTaskStatus(taskID, "cancelled", nil, nil, nil)
	return true
}
