//go:build windows

// internal/extension/chrome.go
package extension

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type ChromeConfig struct {
	Headless       bool     `json:"headless"`
	UserAgent      string   `json:"user_agent"`
	Proxy          string   `json:"proxy"`
	DefaultTimeout int      `json:"default_timeout"`
	ExtraArgs      []string `json:"extra_args"`
}

type ChromeExtension struct {
	mu      sync.Mutex
	config  ChromeConfig
	path    string
	enabled bool
}

func NewChromeExtension(path string, configJSON string, enabled bool) *ChromeExtension {
	c := &ChromeExtension{
		path:    path,
		enabled: enabled,
		config: ChromeConfig{
			Headless:       true,
			DefaultTimeout: 30,
		},
	}
	if configJSON != "" {
		json.Unmarshal([]byte(configJSON), &c.config)
	}
	if c.config.DefaultTimeout == 0 {
		c.config.DefaultTimeout = 30
	}
	return c
}

// DetectChromePath 自动检测 Chrome 路径
func DetectChromePath() string {
	// Windows 常见路径
	paths := []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	}
	// 环境变量
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		paths = append(paths, filepath.Join(localAppData, `Google\Chrome\Application\chrome.exe`))
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// 尝试 Edge 浏览器
	edgePaths := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	}
	for _, p := range edgePaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

func (c *ChromeExtension) IsEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

func (c *ChromeExtension) GetConfig() ChromeConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.config
}

func (c *ChromeExtension) UpdateConfig(config ChromeConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config = config
}

func (c *ChromeExtension) Validate() error {
	if c.path == "" {
		detected := DetectChromePath()
		if detected == "" {
			return fmt.Errorf("未找到 Chrome 浏览器，请配置路径")
		}
		c.path = detected
	}

	if _, err := os.Stat(c.path); err != nil {
		return fmt.Errorf("Chrome 路径不存在: %s", c.path)
	}

	return nil
}

func (c *ChromeExtension) GetExecPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

// SetPath 线程安全地更新 Chrome 可执行文件路径
func (c *ChromeExtension) SetPath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.path = path
}

// SetEnabled 线程安全地更新启用状态
func (c *ChromeExtension) SetEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enabled = enabled
}

func (c *ChromeExtension) ToJSON() (string, error) {
	data, err := json.Marshal(c.config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// initDefaultExtensions 默认注册 Chrome 拓展
func initDefaultExtensions(mgr *Manager) {
	// 检测 Chrome 路径
	chromePath := DetectChromePath()

	chromeConfig := ChromeConfig{
		Headless:       true,
		DefaultTimeout: 30,
	}
	configJSON, _ := json.Marshal(chromeConfig)

	mgr.Register(&Extension{
		Name:   "chrome",
		Title:  "Chrome浏览器",
		Type:   "browser",
		Path:   chromePath,
		Config: string(configJSON),
		Status: 1, // 启用
	})

	// 预留拓展
	mgr.Register(&Extension{
		Name:   "ffmpeg",
		Title:  "FFmpeg",
		Type:   "media",
		Path:   "",
		Config: "{}",
		Status: 0, // 禁用
	})

	mgr.Register(&Extension{
		Name:   "python",
		Title:  "Python",
		Type:   "script",
		Path:   "",
		Config: "{}",
		Status: 0,
	})
}
