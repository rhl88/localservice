package storage

import (
	"path/filepath"
	"testing"
)

// newTestDB 创建临时内存级别的 SQLite 数据库用于测试
func newTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	d, err := NewDB(dbPath, "")
	if err != nil {
		t.Fatalf("NewDB 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = d.Close()
	})
	return d
}

func TestTaskCRUD(t *testing.T) {
	d := newTestDB(t)

	task := &Task{
		ID:     "task-001",
		AppID:  "demo-app",
		Type:   "php",
		Mode:   "sync",
		Params: `{"path":"x"}`,
		Status: "pending",
	}
	if err := d.CreateTask(task); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 转为 running
	if err := d.UpdateTaskStatus("task-001", "running", nil, nil, nil); err != nil {
		t.Fatalf("UpdateTaskStatus running 失败: %v", err)
	}
	got, err := d.GetTask("task-001")
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if got.Status != "running" || got.StartTime == nil {
		t.Fatalf("期望 running 且 StartTime 非空，实际 status=%s startTime=%v", got.Status, got.StartTime)
	}

	// 转为 success
	result := "ok"
	var dur int64 = 1234
	if err := d.UpdateTaskStatus("task-001", "success", &result, nil, &dur); err != nil {
		t.Fatalf("UpdateTaskStatus success 失败: %v", err)
	}
	got, err = d.GetTask("task-001")
	if err != nil {
		t.Fatalf("GetTask 第二次失败: %v", err)
	}
	if got.Status != "success" || got.Result == nil || *got.Result != "ok" || got.DurationMs == nil || *got.DurationMs != 1234 {
		t.Fatalf("期望 success 且 result=ok duration=1234，实际 status=%s result=%v dur=%v", got.Status, got.Result, got.DurationMs)
	}

	// 列表查询
	tasks, total, err := d.ListTasks("demo-app", "", 1, 10)
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if total != 1 || len(tasks) != 1 {
		t.Fatalf("期望 total=1 len=1，实际 total=%d len=%d", total, len(tasks))
	}
}

func TestLogCRUD(t *testing.T) {
	d := newTestDB(t)

	taskID := "task-log-1"
	if err := d.InsertLog("app-1", &taskID, "info", "hello", nil); err != nil {
		t.Fatalf("InsertLog 失败: %v", err)
	}
	if err := d.InsertLog("app-1", nil, "error", "boom", nil); err != nil {
		t.Fatalf("InsertLog 第二条失败: %v", err)
	}

	logs, total, err := d.ListLogs("app-1", "", 1, 10)
	if err != nil {
		t.Fatalf("ListLogs 失败: %v", err)
	}
	if total != 2 || len(logs) != 2 {
		t.Fatalf("期望 total=2 len=2，实际 total=%d len=%d", total, len(logs))
	}

	// 按 level 过滤
	logs, total, err = d.ListLogs("app-1", "error", 1, 10)
	if err != nil {
		t.Fatalf("ListLogs level=error 失败: %v", err)
	}
	if total != 1 || len(logs) != 1 || logs[0].Level != "error" {
		t.Fatalf("期望 error 一条，实际 total=%d logs=%v", total, logs)
	}

	// 清理旧日志（天数 0 不会删除今天的，先用 0 验证 SQL 可执行）
	if err := d.CleanOldLogs(0); err != nil {
		t.Fatalf("CleanOldLogs 失败: %v", err)
	}
}

func TestExtensionUpsertAndGet(t *testing.T) {
	d := newTestDB(t)

	ext := &Extension{
		Name:   "demo",
		Title:  "Demo Extension",
		Type:   "php",
		Status: 1,
	}
	if err := d.UpsertExtension(ext); err != nil {
		t.Fatalf("UpsertExtension 失败: %v", err)
	}

	got, err := d.GetExtension("demo")
	if err != nil {
		t.Fatalf("GetExtension 失败: %v", err)
	}
	if got.Title != "Demo Extension" || got.Status != 1 {
		t.Fatalf("期望 Title=Demo Extension Status=1，实际 Title=%s Status=%d", got.Title, got.Status)
	}

	// 更新已存在
	ext.Title = "Updated"
	if err := d.UpsertExtension(ext); err != nil {
		t.Fatalf("UpsertExtension 更新失败: %v", err)
	}
	got, err = d.GetExtension("demo")
	if err != nil {
		t.Fatalf("GetExtension 更新后失败: %v", err)
	}
	if got.Title != "Updated" {
		t.Fatalf("期望 Title=Updated，实际 Title=%s", got.Title)
	}

	// 列表查询
	exts, err := d.ListExtensions()
	if err != nil {
		t.Fatalf("ListExtensions 失败: %v", err)
	}
	if len(exts) != 1 {
		t.Fatalf("期望 len=1，实际 len=%d", len(exts))
	}

	// UpdateExtensionConfig
	path := "/some/path"
	config := `{"k":"v"}`
	if err := d.UpdateExtensionConfig("demo", &path, &config, 0); err != nil {
		t.Fatalf("UpdateExtensionConfig 失败: %v", err)
	}
	got, err = d.GetExtension("demo")
	if err != nil {
		t.Fatalf("GetExtension 配置更新后失败: %v", err)
	}
	if got.Path == nil || *got.Path != "/some/path" || got.Status != 0 {
		t.Fatalf("期望 path=/some/path status=0，实际 path=%v status=%d", got.Path, got.Status)
	}
}
