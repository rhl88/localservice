//go:build windows

// internal/extension/manager.go
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

type Extension struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Path   string `json:"path"`
	Config string `json:"config"`
	Status int    `json:"status"` // 0=禁用 1=启用
}

// browserInstance 保存单个浏览器实例的上下文与取消函数
type browserInstance struct {
	cancel        context.CancelFunc // allocator 级取消，终止 Chrome 进程
	browserCancel context.CancelFunc // browser 级取消，用于优雅关闭
	ctx           context.Context
}

type Manager struct {
	mu         sync.RWMutex
	extensions map[string]*Extension
	chrome     *ChromeExtension
	browsers   map[string]*browserInstance
}

func NewManager() *Manager {
	m := &Manager{
		extensions: make(map[string]*Extension),
		browsers:   make(map[string]*browserInstance),
	}
	initDefaultExtensions(m)
	return m
}

func (m *Manager) Register(ext *Extension) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.extensions[ext.Name] = ext
}

// SyncFromDB 从数据库加载所有拓展配置到内存中，
// 已存在的同名拓展会被数据库记录覆盖（包括路径、状态等配置），
// 数据库中不存在的默认拓展则保留不变。
func (m *Manager) SyncFromDB(dbExts []*Extension) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, dbExt := range dbExts {
		m.extensions[dbExt.Name] = dbExt

		// 初始化 Chrome 拓展实例
		if dbExt.Name == "chrome" {
			if m.chrome == nil {
				m.chrome = NewChromeExtension(dbExt.Path, dbExt.Config, dbExt.Status == 1)
			} else {
				m.chrome.SetPath(dbExt.Path)
				m.chrome.SetEnabled(dbExt.Status == 1)
				if dbExt.Config != "" {
					var cfg ChromeConfig
					if err := json.Unmarshal([]byte(dbExt.Config), &cfg); err == nil {
						m.chrome.UpdateConfig(cfg)
					}
				}
			}
		}
	}
}

func (m *Manager) List() []*Extension {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []*Extension
	for _, ext := range m.extensions {
		list = append(list, ext)
	}
	return list
}

func (m *Manager) Get(name string) (*Extension, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ext, ok := m.extensions[name]
	return ext, ok
}

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

	if name == "chrome" {
		if m.chrome == nil {
			m.chrome = NewChromeExtension(path, config, status == 1)
		} else {
			m.chrome.SetPath(path)
			m.chrome.SetEnabled(status == 1)
			if config != "" {
				var cfg ChromeConfig
				if err := json.Unmarshal([]byte(config), &cfg); err == nil {
					m.chrome.UpdateConfig(cfg)
				}
			}
		}
	}
}

func (m *Manager) GetChrome() *ChromeExtension {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.chrome
}

func (m *Manager) Test(name string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ext, ok := m.extensions[name]
	if !ok {
		return fmt.Errorf("拓展不存在: %s", name)
	}

	switch ext.Type {
	case "browser":
		chrome := NewChromeExtension(ext.Path, ext.Config, true)
		return chrome.Validate()
	default:
		if ext.Path == "" {
			return fmt.Errorf("路径未配置")
		}
		if _, err := os.Stat(ext.Path); err != nil {
			return fmt.Errorf("路径不存在: %s", ext.Path)
		}
		return nil
	}
}

// OpenBrowser 打开浏览器实例，返回 browserID
func (m *Manager) OpenBrowser(headless bool) (string, error) {
	chrome := m.GetChrome()
	if chrome == nil || !chrome.IsEnabled() {
		return "", fmt.Errorf("Chrome 拓展未启用")
	}
	if err := chrome.Validate(); err != nil {
		return "", fmt.Errorf("Chrome 校验失败: %w", err)
	}

	cfg := chrome.GetConfig()

	// 构建分配器选项
	opts := append([]chromedp.ExecAllocatorOption{},
		chromedp.ExecPath(chrome.GetExecPath()),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		// 反爬检测：隐藏自动化控制特征
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-features", "ChromeWhatsNewUI,HideSidebar,IsolateOrigins,site-per-process"),
		// 稳定性参数（Session 0 服务环境下必需）
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.WindowSize(1920, 1080),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("disable-extensions", false),
		// 避免在 Session 0 中创建不必要的窗口
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-component-extensions-with-background-pages", true),
		chromedp.Flag("disable-ipc-flooding-protection", true),
	)

	if headless {
		// 标准无头模式 + new headless 标记
		opts = append(opts, chromedp.Headless)
	}

	if cfg.UserAgent != "" {
		opts = append(opts, chromedp.UserAgent(cfg.UserAgent))
	}

	if cfg.Proxy != "" {
		opts = append(opts, chromedp.ProxyServer(cfg.Proxy))
	}

	for _, arg := range cfg.ExtraArgs {
		kv := strings.SplitN(arg, "=", 2)
		if len(kv) == 2 {
			opts = append(opts, chromedp.Flag(kv[0], kv[1]))
		} else {
			opts = append(opts, chromedp.Flag(kv[0], true))
		}
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	// 启动浏览器
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		cancel()
		return "", fmt.Errorf("启动浏览器失败: %w", err)
	}

	browserID := uuid.New().String()
	m.mu.Lock()
	m.browsers[browserID] = &browserInstance{
		cancel:        cancel,
		browserCancel: browserCancel,
		ctx:           browserCtx,
	}
	m.mu.Unlock()

	return browserID, nil
}

// GetBrowserContext 获取浏览器上下文
func (m *Manager) GetBrowserContext(browserID string) (context.Context, error) {
	m.mu.RLock()
	inst, ok := m.browsers[browserID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("浏览器实例不存在: %s", browserID)
	}
	return inst.ctx, nil
}

// CloseBrowser 关闭浏览器实例
func (m *Manager) CloseBrowser(browserID string) error {
	m.mu.Lock()
	inst, ok := m.browsers[browserID]
	delete(m.browsers, browserID)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("浏览器实例不存在: %s", browserID)
	}
	inst.browserCancel() // 先优雅关闭浏览器标签页
	inst.cancel()        // 再终止 Chrome 进程并回收 allocator
	return nil
}

// =============================================
//  交互式浏览器（在用户桌面会话中启动 Chrome）
// =============================================

var (
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modwtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
	moduserenv  = windows.NewLazySystemDLL("userenv.dll")

	procWTSGetActiveConsoleSessionId = modkernel32.NewProc("WTSGetActiveConsoleSessionId")
	procWTSQueryUserToken            = modwtsapi32.NewProc("WTSQueryUserToken")
	procGetUserProfileDirectoryW     = moduserenv.NewProc("GetUserProfileDirectoryW")
)

// interactiveBrowser 记录交互式启动的浏览器进程信息
type interactiveBrowser struct {
	cmd       *exec.Cmd
	debugPort int
}

// OpenBrowserInteractive 在用户交互桌面会话中启动 Chrome 并通过 CDP 连接。
// 使用 CreateProcessAsUser Win32 API 绕过 Windows 服务 Session 0 隔离限制，
// 让 Chrome 运行在用户的交互桌面会话中，从而能正确加载 CSS/JS。
func (m *Manager) OpenBrowserInteractive(debugPort int) (string, error) {
	chrome := m.GetChrome()
	if chrome == nil || !chrome.IsEnabled() {
		return "", fmt.Errorf("Chrome 拓展未启用")
	}
	if err := chrome.Validate(); err != nil {
		return "", fmt.Errorf("Chrome 校验失败: %w", err)
	}

	execPath := chrome.GetExecPath()

	// 确保 user-data-dir 目录存在
	userDataDir := filepath.Join(os.TempDir(), "cmspro_chrome_interactive")
	os.MkdirAll(userDataDir, 0755)

	// 构建 Chrome 启动参数
	args := []string{
		"--remote-debugging-port=" + fmt.Sprintf("%d", debugPort),
		"--user-data-dir=" + userDataDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-blink-features=AutomationControlled",
		"--disable-features=ChromeWhatsNewUI,HideSidebar,IsolateOrigins,site-per-process",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--disable-infobars",
		"--window-size=1920,1080",
		"--disable-gpu",
	}

	cfg := chrome.GetConfig()
	if cfg.UserAgent != "" {
		args = append(args, "--user-agent="+cfg.UserAgent)
	}
	if cfg.Proxy != "" {
		args = append(args, "--proxy-server="+cfg.Proxy)
	}
	for _, arg := range cfg.ExtraArgs {
		args = append(args, arg)
	}

	// 在用户交互会话中启动 Chrome
	pid, err := startProcessInUserSession(execPath, args)
	if err != nil {
		return "", fmt.Errorf("在用户会话中启动 Chrome 失败: %w", err)
	}

	// 等待 Chrome 启动并监听调试端口
	debugURL := fmt.Sprintf("http://127.0.0.1:%d", debugPort)
	if err := waitForChrome(debugURL, 15*time.Second); err != nil {
		// 启动失败，清理进程
		killProcess(pid)
		return "", fmt.Errorf("等待 Chrome 调试端口就绪失败: %w", err)
	}

	// 通过 CDP 连接到已启动的 Chrome
	allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), debugURL)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	// 测试连接
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		cancel()
		killProcess(pid)
		return "", fmt.Errorf("CDP 连接失败: %w", err)
	}

	browserID := uuid.New().String()
	m.mu.Lock()
	m.browsers[browserID] = &browserInstance{
		cancel:        cancel,
		browserCancel: browserCancel,
		ctx:           browserCtx,
	}
	m.mu.Unlock()

	return browserID, nil
}

// startProcessInUserSession 使用 CreateProcessAsUser 在用户交互桌面会话中启动进程
func startProcessInUserSession(exePath string, args []string) (uint32, error) {
	// 1. 获取当前活跃的控制台会话 ID
	sessionId, _, _ := procWTSGetActiveConsoleSessionId.Call()
	if sessionId == 0xFFFFFFFF {
		return 0, fmt.Errorf("没有活跃的控制台会话（可能没有用户登录）")
	}

	// 2. 获取该会话的用户 Token
	var userToken windows.Token
	ret, _, err := procWTSQueryUserToken.Call(sessionId, uintptr(unsafe.Pointer(&userToken)))
	if ret == 0 {
		return 0, fmt.Errorf("WTSQueryUserToken 失败: %w", err)
	}
	defer userToken.Close()

	// 3. 复制 Token
	var dupToken windows.Token
	err = windows.DuplicateTokenEx(userToken, windows.TOKEN_ALL_ACCESS, nil,
		windows.SecurityDelegation, windows.TokenPrimary, &dupToken)
	if err != nil {
		return 0, fmt.Errorf("DuplicateTokenEx 失败: %w", err)
	}
	defer dupToken.Close()

	// 4. 获取用户环境变量
	var envBlock *uint16
	err = windows.CreateEnvironmentBlock(&envBlock, dupToken, false)
	if err != nil {
		return 0, fmt.Errorf("CreateEnvironmentBlock 失败: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(envBlock)

	// 5. 构建命令行
	cmdLine := exePath
	for _, a := range args {
		if strings.Contains(a, " ") {
			cmdLine += ` "` + a + `"`
		} else {
			cmdLine += " " + a
		}
	}

	cmdLinePtr, _ := windows.UTF16PtrFromString(cmdLine)

	// 6. 创建进程
	var startupInfo windows.StartupInfo
	startupInfo.Cb = uint32(unsafe.Sizeof(startupInfo))
	startupInfo.Flags = windows.STARTF_USESHOWWINDOW
	startupInfo.ShowWindow = windows.SW_SHOW

	var procInfo windows.ProcessInformation

	err = windows.CreateProcessAsUser(
		dupToken,
		nil,
		cmdLinePtr,
		nil,
		nil,
		false,
		windows.CREATE_NEW_CONSOLE|windows.CREATE_UNICODE_ENVIRONMENT,
		envBlock,
		nil,
		&startupInfo,
		&procInfo,
	)
	if err != nil {
		return 0, fmt.Errorf("CreateProcessAsUser 失败: %w", err)
	}

	defer windows.CloseHandle(procInfo.Process)
	defer windows.CloseHandle(procInfo.Thread)

	return procInfo.ProcessId, nil
}

// waitForChrome 等待 Chrome 调试端口就绪
func waitForChrome(debugURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := httpDefaultClientGet(debugURL + "/json/version")
		if err == nil && resp != "" {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("超时 %v", timeout)
}

// httpDefaultClientGet 简单 HTTP GET（用于检测 Chrome 调试端口）
func httpDefaultClientGet(url string) (string, error) {
	cmd := exec.Command("cmd", "/c", "curl", "-s", "-m", "2", url)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// killProcess 通过 PID 终止进程
func killProcess(pid uint32) {
	proc, err := os.FindProcess(int(pid))
	if err == nil {
		proc.Kill()
	}
}
