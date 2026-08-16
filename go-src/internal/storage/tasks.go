package storage

import (
	"database/sql"
)

// Task 任务记录实体，对应 tasks 表
type Task struct {
	ID         string  `json:"id"`
	AppID      string  `json:"app_id"`
	Type       string  `json:"type"`
	Mode       string  `json:"mode"`
	Params     string  `json:"params"`
	Status     string  `json:"status"`
	Result     *string `json:"result"`
	Error      *string `json:"error"`
	CreateTime string  `json:"create_time"`
	StartTime  *string `json:"start_time"`
	FinishTime *string `json:"finish_time"`
	DurationMs *int64  `json:"duration_ms"`
}

// CreateTask 创建一条任务记录
func (d *DB) CreateTask(t *Task) error {
	_, err := d.Exec(
		`INSERT INTO tasks (id, app_id, type, mode, params, status, create_time) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.AppID, t.Type, t.Mode, t.Params, t.Status, d.now(),
	)
	return err
}

// UpdateTaskStatus 更新任务状态
// status 为 running 时只更新开始时间；其他终态写入结果、错误、结束时间和耗时
func (d *DB) UpdateTaskStatus(id string, status string, result *string, errMsg *string, durationMs *int64) error {
	now := d.now()
	if status == "running" {
		_, err := d.Exec(`UPDATE tasks SET status = ?, start_time = ? WHERE id = ?`, status, now, id)
		return err
	}
	_, err := d.Exec(
		`UPDATE tasks SET status = ?, result = ?, error = ?, finish_time = ?, duration_ms = ? WHERE id = ?`,
		status, result, errMsg, now, durationMs, id,
	)
	return err
}

// GetTask 根据 ID 查询单个任务
func (d *DB) GetTask(id string) (*Task, error) {
	t := &Task{}
	var result, errMsg, startTime, finishTime sql.NullString
	var durationMs sql.NullInt64
	err := d.QueryRow(
		`SELECT id, app_id, type, mode, params, status, result, error, create_time, start_time, finish_time, duration_ms FROM tasks WHERE id = ?`,
		id,
	).Scan(&t.ID, &t.AppID, &t.Type, &t.Mode, &t.Params, &t.Status, &result, &errMsg, &t.CreateTime, &startTime, &finishTime, &durationMs)
	if err != nil {
		return nil, err
	}
	t.Result = nilIfNull(result)
	t.Error = nilIfNull(errMsg)
	t.StartTime = nilIfNull(startTime)
	t.FinishTime = nilIfNull(finishTime)
	if durationMs.Valid {
		v := durationMs.Int64
		t.DurationMs = &v
	}
	return t, nil
}

// ListTasks 分页查询任务列表，支持按 appID 和 status 过滤
// 返回任务列表和总数
func (d *DB) ListTasks(appID string, status string, page int, pageSize int) ([]*Task, int, error) {
	offset := (page - 1) * pageSize
	var args []interface{}
	query := `SELECT id, app_id, type, mode, params, status, result, error, create_time, start_time, finish_time, duration_ms FROM tasks WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM tasks WHERE 1=1`

	if appID != "" {
		query += ` AND app_id = ?`
		countQuery += ` AND app_id = ?`
		args = append(args, appID)
	}
	if status != "" {
		query += ` AND status = ?`
		countQuery += ` AND status = ?`
		args = append(args, status)
	}

	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	err := d.QueryRow(countQuery, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query += ` ORDER BY create_time DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, offset)
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		var result, errMsg, startTime, finishTime sql.NullString
		var durationMs sql.NullInt64
		if err := rows.Scan(&t.ID, &t.AppID, &t.Type, &t.Mode, &t.Params, &t.Status, &result, &errMsg, &t.CreateTime, &startTime, &finishTime, &durationMs); err != nil {
			return nil, 0, err
		}
		t.Result = nilIfNull(result)
		t.Error = nilIfNull(errMsg)
		t.StartTime = nilIfNull(startTime)
		t.FinishTime = nilIfNull(finishTime)
		if durationMs.Valid {
			v := durationMs.Int64
			t.DurationMs = &v
		}
		tasks = append(tasks, t)
	}
	return tasks, total, nil
}

// ClearTasks 清空全部任务记录
func (d *DB) ClearTasks() error {
	_, err := d.Exec(`DELETE FROM tasks`)
	return err
}

// nilIfNull 将 sql.NullString 转换为 *string，无效值返回 nil
// 同包内 tasks.go、logs.go、extensions.go 共用此辅助函数
func nilIfNull(s sql.NullString) *string {
	if s.Valid {
		return &s.String
	}
	return nil
}
