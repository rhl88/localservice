package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB 封装 sql.DB，提供 SQLite 连接管理与表初始化能力
type DB struct {
	*sql.DB
	loc *time.Location // 时区配置，影响所有时间字段的生成
}

// NewDB 创建并初始化 SQLite 数据库连接
// dbPath 为数据库文件路径，目录不存在时自动创建
// timezone 参数指定时区名称（如 "Asia/Shanghai"），为空时使用系统默认时区
func NewDB(dbPath string, timezone string) (*DB, error) {
	// 确保目录存在
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建数据库目录失败: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// SQLite 单写特性，连接池设为 1
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("数据库连接测试失败: %w", err)
	}

	// 解析时区配置
	var loc *time.Location
	if timezone != "" {
		loc, err = time.LoadLocation(timezone)
		if err != nil {
			return nil, fmt.Errorf("无效的时区配置 %q: %w", timezone, err)
		}
	}

	d := &DB{DB: db, loc: loc}
	if err := d.initTables(); err != nil {
		return nil, fmt.Errorf("初始化表失败: %w", err)
	}

	return d, nil
}

// initTables 创建任务、日志、拓展相关表及索引（已存在则跳过）
func (d *DB) initTables() error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			type TEXT NOT NULL,
			mode TEXT NOT NULL,
			params TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			result TEXT,
			error TEXT,
			create_time DATETIME DEFAULT CURRENT_TIMESTAMP,
			start_time DATETIME,
			finish_time DATETIME,
			duration_ms INTEGER
		);

		CREATE TABLE IF NOT EXISTS logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT,
			app_id TEXT NOT NULL,
			level TEXT NOT NULL,
			message TEXT NOT NULL,
			context TEXT,
			create_time DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS extensions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			type TEXT NOT NULL,
			path TEXT,
			config TEXT,
			status INTEGER DEFAULT 1,
			create_time DATETIME DEFAULT CURRENT_TIMESTAMP,
			update_time DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_tasks_app_id ON tasks(app_id);
		CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
		CREATE INDEX IF NOT EXISTS idx_logs_app_id ON logs(app_id);
	`)
	return err
}

// now 返回配置时区的当前时间格式化字符串
// loc 未设置时回退到 time.Local
// 返回格式如 "2006-01-02 15:04:05 +08:00"，带时区偏移以兼容 modernc.org/sqlite
// 驱动自动将 DATETIME 文本扫描为 time.Time 时的时区识别
func (d *DB) now() string {
	loc := d.loc
	if loc == nil {
		loc = time.Local
	}
	return time.Now().In(loc).Format("2006-01-02 15:04:05 -07:00")
}
