package storage

import (
	"database/sql"
)

// Extension 拓展配置实体，对应 extensions 表
type Extension struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Title      string  `json:"title"`
	Type       string  `json:"type"`
	Path       *string `json:"path"`
	Config     *string `json:"config"`
	Status     int     `json:"status"`
	CreateTime string  `json:"create_time"`
	UpdateTime string  `json:"update_time"`
}

// GetExtension 根据 name 查询单个拓展配置
func (d *DB) GetExtension(name string) (*Extension, error) {
	e := &Extension{}
	var path, config sql.NullString
	err := d.QueryRow(
		`SELECT id, name, title, type, path, config, status, create_time, update_time FROM extensions WHERE name = ?`,
		name,
	).Scan(&e.ID, &e.Name, &e.Title, &e.Type, &path, &config, &e.Status, &e.CreateTime, &e.UpdateTime)
	if err != nil {
		return nil, err
	}
	e.Path = nilIfNull(path)
	e.Config = nilIfNull(config)
	return e, nil
}

// ListExtensions 查询全部拓展配置，按 id 升序
func (d *DB) ListExtensions() ([]*Extension, error) {
	rows, err := d.Query(`SELECT id, name, title, type, path, config, status, create_time, update_time FROM extensions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var exts []*Extension
	for rows.Next() {
		e := &Extension{}
		var path, config sql.NullString
		if err := rows.Scan(&e.ID, &e.Name, &e.Title, &e.Type, &path, &config, &e.Status, &e.CreateTime, &e.UpdateTime); err != nil {
			return nil, err
		}
		e.Path = nilIfNull(path)
		e.Config = nilIfNull(config)
		exts = append(exts, e)
	}
	return exts, nil
}

// UpsertExtension 插入或更新拓展配置
// name 字段冲突时触发更新
func (d *DB) UpsertExtension(e *Extension) error {
	now := d.now()
	_, err := d.Exec(
		`INSERT INTO extensions (name, title, type, path, config, status, create_time, update_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET title=?, type=?, path=?, config=?, status=?, update_time=?`,
		e.Name, e.Title, e.Type, e.Path, e.Config, e.Status, now, now,
		e.Title, e.Type, e.Path, e.Config, e.Status, now,
	)
	return err
}

// UpdateExtensionConfig 更新指定 name 拓展的路径、配置和状态
func (d *DB) UpdateExtensionConfig(name string, path *string, config *string, status int) error {
	now := d.now()
	_, err := d.Exec(
		`UPDATE extensions SET path=?, config=?, status=?, update_time=? WHERE name=?`,
		path, config, status, now, name,
	)
	return err
}
