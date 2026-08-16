//go:build windows

// internal/extension/defaults_windows.go
package extension

import (
	"encoding/json"
	"log"

	"cmspro-local-service/internal/storage"
)

// defaultExtensions 返回默认的拓展列表。
func defaultExtensions() []*Extension {
	chromeConfig := ChromeConfig{
		Headless:       true,
		DefaultTimeout: 30,
	}
	configJSON, _ := json.Marshal(chromeConfig)

	return []*Extension{
		{
			Name:   "chrome",
			Title:  "Chrome浏览器",
			Type:   "browser",
			Path:   "",
			Config: string(configJSON),
			Status: 1,
		},
		{
			Name:   "ffmpeg",
			Title:  "FFmpeg",
			Type:   "media",
			Path:   "",
			Config: "{}",
			Status: 0,
		},
		{
			Name:   "python",
			Title:  "Python",
			Type:   "script",
			Path:   "",
			Config: "{}",
			Status: 0,
		},
	}
}

// SyncDefaultsToDB 确保数据库 extensions 表中包含默认拓展记录。
// 仅当某拓展在数据库中不存在时，才插入默认记录。
// 已存在的拓展不会被覆盖，以保留用户已保存的配置。
func SyncDefaultsToDB(db *storage.DB, mgr *Manager) {
	defaults := defaultExtensions()
	for _, ext := range defaults {
		existing, err := db.GetExtension(ext.Name)
		if err != nil || existing == nil {
			path := nullableString(ext.Path)
			config := nullableString(ext.Config)
			dbExt := &storage.Extension{
				Name:   ext.Name,
				Title:  ext.Title,
				Type:   ext.Type,
				Path:   path,
				Config: config,
				Status: ext.Status,
			}
			if err := db.UpsertExtension(dbExt); err != nil {
				log.Printf("插入默认拓展 %s 失败: %v", ext.Name, err)
			} else {
				log.Printf("已插入默认拓展: %s", ext.Name)
			}
		}
	}
}

// nullableString 将 Go string 转为 *string，空字符串转为 nil
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
