//go:build !windows

// Package extension 在 Linux 平台提供空存根实现。
// Linux 端本地服务仅提供脚本执行等后台能力，不需要浏览器自动化等 Windows 桌面功能。
package extension

import (
	"context"
	"sync"
)

// Extension 表示一个拓展配置
type Extension struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Path   string `json:"path"`
	Config string `json:"config"`
	Status int    `json:"status"`
}

// ChromeExtension 的 Linux 存根（仅实现接口签名）
type ChromeExtension struct{}

func (c *ChromeExtension) IsEnabled() bool  { return false }
func (c *ChromeExtension) Validate() error  { return nil }
func (c *ChromeExtension) GetExecPath() string { return "" }

// Manager 拓展管理器（Linux 空存根）
// Manager 拓展管理器（Linux 使用内存存储，支持从数据库同步）
type Manager struct {
	extensions map[string]*Extension
	mu         sync.RWMutex
}

// NewManager 创建空拓展管理器
func NewManager() *Manager {
	return &Manager{
		extensions: make(map[string]*Extension),
	}
}

// Register 注册拓展
func (m *Manager) Register(ext *Extension) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extensions[ext.Name] = ext
}

// List 返回所有已注册的拓展
func (m *Manager) List() []*Extension {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []*Extension
	for _, ext := range m.extensions {
		list = append(list, ext)
	}
	return list
}

// Get 根据名称获取拓展
func (m *Manager) Get(name string) (*Extension, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ext, ok := m.extensions[name]
	return ext, ok
}

// Update 更新拓展配置
func (m *Manager) Update(name string, path string, config string, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ext, ok := m.extensions[name]
	if !ok {
		return
	}
	ext.Path = path
	ext.Config = config
	ext.Status = status
}

// SyncFromDB 从数据库加载所有拓展配置到内存中
func (m *Manager) SyncFromDB(dbExts []*Extension) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, dbExt := range dbExts {
		m.extensions[dbExt.Name] = dbExt
	}
}

// GetChrome 返回 nil
func (m *Manager) GetChrome() *ChromeExtension {
	return nil
}

// Test 返回 nil
func (m *Manager) Test(name string) error {
	return nil
}

// OpenBrowser 返回空字符串
func (m *Manager) OpenBrowser(headless bool) (string, error) {
	return "", nil
}

// GetBrowserContext 返回 nil
func (m *Manager) GetBrowserContext(browserID string) (context.Context, error) {
	return nil, nil
}

// CloseBrowser 返回 nil
func (m *Manager) CloseBrowser(browserID string) error {
	return nil
}

// OpenBrowserInteractive 返回空字符串
func (m *Manager) OpenBrowserInteractive(debugPort int) (string, error) {
	return "", nil
}
