// internal/task/browser.go
package task

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"cmspro-local-service/internal/extension"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// extMgr 拓展管理器，由 main 注入
var extMgr *extension.Manager

// SetExtensionManager 注入拓展管理器
func SetExtensionManager(m *extension.Manager) {
	extMgr = m
}

// BrowserOpenHandler 打开浏览器实例
type BrowserOpenHandler struct{}

func (h *BrowserOpenHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	if extMgr == nil {
		return nil, fmt.Errorf("拓展管理器未初始化")
	}

	headless := true
	if hl, ok := params["headless"].(bool); ok {
		headless = hl
	}

	browserID, err := extMgr.OpenBrowser(headless)
	if err != nil {
		return nil, err
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"browser_id": browserID},
	}, nil
}

// BrowserOpenInteractiveHandler 在用户交互桌面会话中启动浏览器
type BrowserOpenInteractiveHandler struct{}

func (h *BrowserOpenInteractiveHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	if extMgr == nil {
		return nil, fmt.Errorf("拓展管理器未初始化")
	}

	debugPort := 9222
	if dp, ok := params["debug_port"].(float64); ok && dp > 0 {
		debugPort = int(dp)
	}

	browserID, err := extMgr.OpenBrowserInteractive(debugPort)
	if err != nil {
		return nil, err
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"browser_id": browserID},
	}, nil
}

// BrowserNavigateHandler 导航到指定 URL
type BrowserNavigateHandler struct{}

func (h *BrowserNavigateHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	url, _ := params["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("缺少 url 参数")
	}

	waitSec := 0
	if w, ok := params["wait_seconds"].(float64); ok {
		waitSec = int(w)
	}

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	var actions []chromedp.Action
	actions = append(actions, chromedp.Navigate(url))
	if waitSec > 0 {
		actions = append(actions, chromedp.Sleep(time.Duration(waitSec)*time.Second))
	}

	if err := chromedp.Run(browserCtx, actions...); err != nil {
		return nil, fmt.Errorf("导航失败: %w", err)
	}

	// 获取当前页面标题
	var title string
	chromedp.Run(browserCtx, chromedp.Title(&title))

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"url": url, "title": title},
	}, nil
}

// BrowserScreenshotHandler 页面或元素截图
type BrowserScreenshotHandler struct{}

func (h *BrowserScreenshotHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	selector, _ := params["selector"].(string)

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	var buf []byte
	if selector != "" {
		err = chromedp.Run(browserCtx, chromedp.Screenshot(selector, &buf, chromedp.ByQuery))
	} else {
		err = chromedp.Run(browserCtx, chromedp.CaptureScreenshot(&buf))
	}
	if err != nil {
		return nil, fmt.Errorf("截图失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data: map[string]interface{}{
			"image":     base64.StdEncoding.EncodeToString(buf),
			"mime_type": "image/png",
		},
	}, nil
}

// BrowserExecuteHandler 执行 JavaScript
type BrowserExecuteHandler struct{}

func (h *BrowserExecuteHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	script, _ := params["script"].(string)
	if script == "" {
		return nil, fmt.Errorf("缺少 script 参数")
	}
	awaitPromise, _ := params["await_promise"].(bool)

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	var result interface{}
	if awaitPromise {
		// 异步脚本：使用 chromedp.Evaluate + WithAwaitPromise 自定义选项
		// 复用 chromedp 内置的 parseRemoteObject 结果解析逻辑
		err = chromedp.Run(browserCtx, chromedp.Evaluate(script, &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		}))
		if err != nil {
			return nil, fmt.Errorf("执行异步JavaScript失败: %w", err)
		}
	} else {
		err = chromedp.Run(browserCtx, chromedp.Evaluate(script, &result))
		if err != nil {
			return nil, fmt.Errorf("执行JavaScript失败: %w", err)
		}
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"result": result},
	}, nil
}

// BrowserGetCookiesHandler 获取当前页面 Cookie
type BrowserGetCookiesHandler struct{}

func (h *BrowserGetCookiesHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	var cookieList []map[string]interface{}
	err = chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		cs, err := network.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		for _, c := range cs {
			cookieList = append(cookieList, map[string]interface{}{
				"name":   c.Name,
				"value":  c.Value,
				"domain": c.Domain,
				"path":   c.Path,
			})
		}
		return nil
	}))
	if err != nil {
		return nil, fmt.Errorf("获取Cookie失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"cookies": cookieList},
	}, nil
}

// BrowserCloseHandler 关闭浏览器实例
type BrowserCloseHandler struct{}

func (h *BrowserCloseHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	if browserID == "" {
		return nil, fmt.Errorf("缺少 browser_id 参数")
	}

	if err := extMgr.CloseBrowser(browserID); err != nil {
		return nil, err
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"closed": true},
	}, nil
}

func init() {
	Register("browser.open", &BrowserOpenHandler{})
	Register("browser.open_interactive", &BrowserOpenInteractiveHandler{})
	Register("browser.navigate", &BrowserNavigateHandler{})
	Register("browser.screenshot", &BrowserScreenshotHandler{})
	Register("browser.execute", &BrowserExecuteHandler{})
	Register("browser.get_cookies", &BrowserGetCookiesHandler{})
	Register("browser.set_cookies", &BrowserSetCookiesHandler{})
	Register("browser.click", &BrowserClickHandler{})
	Register("browser.input", &BrowserInputHandler{})
	Register("browser.wait_for", &BrowserWaitForHandler{})
	Register("browser.close", &BrowserCloseHandler{})
}

// BrowserSetCookiesHandler 设置一组 Cookie
type BrowserSetCookiesHandler struct{}

func (h *BrowserSetCookiesHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	cookiesRaw, ok := params["cookies"].([]interface{})
	if !ok || len(cookiesRaw) == 0 {
		return nil, fmt.Errorf("缺少 cookies 参数")
	}

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	err = chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		for _, c := range cookiesRaw {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := cm["name"].(string)
			value, _ := cm["value"].(string)
			if name == "" {
				continue
			}
			param := network.SetCookie(name, value)
			if domain, ok := cm["domain"].(string); ok && domain != "" {
				param = param.WithDomain(domain)
			}
			if path, ok := cm["path"].(string); ok && path != "" {
				param = param.WithPath(path)
			}
			if err := param.Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}))
	if err != nil {
		return nil, fmt.Errorf("设置Cookie失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"count": len(cookiesRaw)},
	}, nil
}

// BrowserClickHandler 点击指定选择器元素
type BrowserClickHandler struct{}

func (h *BrowserClickHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	selector, _ := params["selector"].(string)
	if selector == "" {
		return nil, fmt.Errorf("缺少 selector 参数")
	}

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	if err := chromedp.Run(browserCtx, chromedp.Click(selector, chromedp.ByQuery)); err != nil {
		return nil, fmt.Errorf("点击失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"clicked": true, "selector": selector},
	}, nil
}

// BrowserInputHandler 向指定选择器元素输入文本
type BrowserInputHandler struct{}

func (h *BrowserInputHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	selector, _ := params["selector"].(string)
	value, _ := params["value"].(string)
	if selector == "" {
		return nil, fmt.Errorf("缺少 selector 参数")
	}

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	if err := chromedp.Run(browserCtx, chromedp.SendKeys(selector, value, chromedp.ByQuery)); err != nil {
		return nil, fmt.Errorf("输入失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"input": true, "selector": selector},
	}, nil
}

// BrowserWaitForHandler 等待指定选择器元素可见
type BrowserWaitForHandler struct{}

func (h *BrowserWaitForHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	selector, _ := params["selector"].(string)
	if selector == "" {
		return nil, fmt.Errorf("缺少 selector 参数")
	}

	timeoutSec := 30
	if t, ok := params["timeout"].(float64); ok && t > 0 {
		timeoutSec = int(t)
	}

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(browserCtx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	if err := chromedp.Run(waitCtx, chromedp.WaitVisible(selector, chromedp.ByQuery)); err != nil {
		return nil, fmt.Errorf("等待元素超时或失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"visible": true, "selector": selector},
	}, nil
}

// BrowserSetHeadersHandler 设置额外 HTTP 请求头
type BrowserSetHeadersHandler struct{}

func (h *BrowserSetHeadersHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	browserID, _ := params["browser_id"].(string)
	if browserID == "" {
		return nil, fmt.Errorf("缺少 browser_id 参数")
	}
	headersRaw, ok := params["headers"].(map[string]interface{})
	if !ok || len(headersRaw) == 0 {
		return nil, fmt.Errorf("缺少 headers 参数")
	}

	browserCtx, err := extMgr.GetBrowserContext(browserID)
	if err != nil {
		return nil, err
	}

	// 验证 headers 参数是否有有效字符串值
	headerMap := make(map[string]interface{})
	for k, v := range headersRaw {
		if sv, ok := v.(string); ok {
			headerMap[k] = sv
		}
	}
	if len(headerMap) == 0 {
		return nil, fmt.Errorf("headers 参数无有效字符串值")
	}

	err = chromedp.Run(browserCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		// 先启用 Network 域
		if err := network.Enable().Do(ctx); err != nil {
			return err
		}
		// 设置额外请求头
		return network.SetExtraHTTPHeaders(network.Headers(headerMap)).Do(ctx)
	}))
	if err != nil {
		return nil, fmt.Errorf("设置请求头失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"count": len(headerMap)},
	}, nil
}

func init() {
	Register("browser.set_headers", &BrowserSetHeadersHandler{})
}
