package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Config 本地服务配置项
type Config struct {
	Port             string `json:"port"`
	AuthToken        string `json:"auth_token"`
	LogLevel         string `json:"log_level"`
	LogRetentionDays int    `json:"log_retention_days"`
	DBPath           string `json:"db_path"`
	OpenAPIEnabled   bool   `json:"open_api_enabled"`
	OpenAPIKey       string `json:"open_api_key"`
	Timezone         string `json:"timezone"`
	CmsproBaseURL    string `json:"cmspro_base_url"`
}

var (
	current *Config
	mu      sync.RWMutex
)

// Default 返回默认配置
func Default() *Config {
	return &Config{
		Port:             "9501",
		AuthToken:        "",
		LogLevel:         "info",
		LogRetentionDays: 30,
		DBPath:           "storage/local_service.db",
		Timezone:         "Asia/Shanghai",
	}
}

// Load 从指定路径加载配置文件
// 文件不存在时返回默认配置（不视为错误）
func Load(path string) (*Config, error) {
	mu.Lock()
	defer mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c := Default()
			current = c
			return c, nil
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 字段为空时自动填充默认值
	if c.Port == "" {
		c.Port = "9501"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.LogRetentionDays == 0 {
		c.LogRetentionDays = 30
	}
	if c.DBPath == "" {
		c.DBPath = "storage/local_service.db"
	}
	if c.Timezone == "" {
		c.Timezone = "Asia/Shanghai"
	}
	if c.CmsproBaseURL == "" {
		c.CmsproBaseURL = "http://www.cmspro.com"
	}

	current = &c
	return &c, nil
}

// Get 获取当前配置，未加载时返回默认配置
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return Default()
	}
	return current
}

// Save 将配置写入指定路径，自动创建目录
func Save(path string, c *Config) error {
	mu.Lock()
	defer mu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	current = c
	return nil
}
