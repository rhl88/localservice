package storage

import (
	"database/sql"
	"fmt"
)

// LogEntry 日志实体，对应 logs 表
type LogEntry struct {
	ID         int64   `json:"id"`
	TaskID     *string `json:"task_id"`
	AppID      string  `json:"app_id"`
	Level      string  `json:"level"`
	Message    string  `json:"message"`
	Context    *string `json:"context"`
	CreateTime string  `json:"create_time"`
}

// InsertLog 写入一条日志记录
func (d *DB) InsertLog(appID string, taskID *string, level string, message string, context *string) error {
	_, err := d.Exec(
		`INSERT INTO logs (task_id, app_id, level, message, context, create_time) VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, appID, level, message, context, d.now(),
	)
	return err
}

// ListLogs 分页查询日志列表，支持按 appID 和 level 过滤
// 返回日志列表和总数
func (d *DB) ListLogs(appID string, level string, page int, pageSize int) ([]*LogEntry, int, error) {
	offset := (page - 1) * pageSize
	var args []interface{}
	query := `SELECT id, task_id, app_id, level, message, context, create_time FROM logs WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM logs WHERE 1=1`

	if appID != "" {
		query += ` AND app_id = ?`
		countQuery += ` AND app_id = ?`
		args = append(args, appID)
	}
	if level != "" {
		query += ` AND level = ?`
		countQuery += ` AND level = ?`
		args = append(args, level)
	}

	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	if err := d.QueryRow(countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query += ` ORDER BY create_time DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, offset)
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []*LogEntry
	for rows.Next() {
		l := &LogEntry{}
		var taskID, context sql.NullString
		if err := rows.Scan(&l.ID, &taskID, &l.AppID, &l.Level, &l.Message, &context, &l.CreateTime); err != nil {
			return nil, 0, err
		}
		l.TaskID = nilIfNull(taskID)
		l.Context = nilIfNull(context)
		logs = append(logs, l)
	}
	return logs, total, nil
}

// ClearLogs 清空全部日志（谨慎使用，仅在调试时调用）
func (d *DB) ClearLogs() error {
	_, err := d.Exec(`DELETE FROM logs`)
	return err
}

// CleanOldLogs 清理指定天数之前的日志
func (d *DB) CleanOldLogs(days int) error {
	_, err := d.Exec(
		`DELETE FROM logs WHERE create_time < datetime('now', ?)`,
		fmt.Sprintf("-%d days", days),
	)
	return err
}
