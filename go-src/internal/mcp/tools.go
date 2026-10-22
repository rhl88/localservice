// internal/mcp/tools.go
//
// MCP 工具集：把本地服务既有的任务处理器（task.Registry 中注册的 33 个任务）
// 映射为 MCP 工具，并声明风险等级与确认要求。
//
// 风险分级原则（对齐宝塔 MCP）：
//   - low    只读查询（系统信息、文件读取、进程/服务列表、浏览器只读操作）
//   - medium 有副作用但可恢复（服务启停、浏览器点击输入、Cookie 设置）
//   - high   破坏性或高影响（文件写删、进程启停杀、执行命令/脚本）→ 默认需确认
package mcp

import (
	"context"
	"encoding/json"

	"cmspro-local-service/internal/task"
)

// RegisterBuiltinTools 注册本地服务内置工具集（进程启动时调用一次）
func RegisterBuiltinTools(reg *Registry) error {
	tools := builtinTools()
	for _, t := range tools {
		if err := reg.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// builtinTools 内置工具定义清单
func builtinTools() []*Tool {
	return []*Tool{
		// ============ 系统（分类：系统） ============
		{
			ToolID:      "SystemInfo",
			TaskType:    "system.info",
			NameCN:      "获取系统信息",
			Description: "获取本机系统资源概况。只读。返回操作系统、主机名、CPU 核数、内存总量与可用量、运行时长等基础信息。",
			InputSchema: ObjSchema(nil),
			RiskLevel:   RiskLow,
			Category:    "系统",
		},
		{
			ToolID:      "SystemNetwork",
			TaskType:    "system.network",
			NameCN:      "获取网络信息",
			Description: "获取本机网络接口与地址信息。只读。返回各网卡名称、IP 地址、MAC 地址等。",
			InputSchema: ObjSchema(nil),
			RiskLevel:   RiskLow,
			Category:    "系统",
		},
		{
			ToolID:      "SystemExec",
			TaskType:    "system.exec",
			NameCN:      "执行系统命令",
			Description: "在本机执行系统命令并返回输出。高风险：可执行任意命令，调用前必须向用户确认命令内容与影响。",
			InputSchema: ObjSchema(map[string]interface{}{
				"command": Str("要执行的完整命令"),
				"timeout": Int("超时秒数（可选，默认 30）"),
			}, "command"),
			RiskLevel: RiskHigh,
			Category:  "系统",
			Confirm:   true,
			Timeout:   60,
		},
		{
			ToolID:      "SystemScript",
			TaskType:    "system.script",
			NameCN:      "执行脚本",
			Description: "执行本机已登记的脚本。高风险：脚本内容可能修改系统，调用前必须向用户确认。",
			InputSchema: ObjSchema(map[string]interface{}{
				"name": Str("脚本名称或标识"),
				"args": Str("传给脚本的参数（可选）"),
			}, "name"),
			RiskLevel: RiskHigh,
			Category:  "系统",
			Confirm:   true,
			Timeout:   120,
		},

		// ============ 文件（分类：文件） ============
		{
			ToolID:      "FileRead",
			TaskType:    "file.read",
			NameCN:      "读取文件",
			Description: "读取本机指定路径的文本文件内容。只读。建议先用 FileInfo 确认文件大小，大文件请分段读取。",
			InputSchema: ObjSchema(map[string]interface{}{
				"path":   Str("文件绝对路径"),
				"offset": Int("起始偏移字节（可选）"),
				"limit":  Int("读取字节上限（可选，默认 1MB）"),
			}, "path"),
			RiskLevel: RiskLow,
			Category:  "文件",
		},
		{
			ToolID:      "FileList",
			TaskType:    "file.list",
			NameCN:      "列出目录",
			Description: "列出本机指定目录下的文件与子目录。只读。返回名称、大小、是否为目录等。",
			InputSchema: ObjSchema(map[string]interface{}{
				"path": Str("目录绝对路径"),
			}, "path"),
			RiskLevel: RiskLow,
			Category:  "文件",
		},
		{
			ToolID:      "FileExists",
			TaskType:    "file.exists",
			NameCN:      "判断文件是否存在",
			Description: "判断本机指定路径是否存在。只读。",
			InputSchema: ObjSchema(map[string]interface{}{
				"path": Str("文件或目录绝对路径"),
			}, "path"),
			RiskLevel: RiskLow,
			Category:  "文件",
		},
		{
			ToolID:      "FileInfo",
			TaskType:    "file.info",
			NameCN:      "获取文件信息",
			Description: "获取本机指定路径的详细信息。只读。返回大小、权限、修改时间、是否为目录等。",
			InputSchema: ObjSchema(map[string]interface{}{
				"path": Str("文件或目录绝对路径"),
			}, "path"),
			RiskLevel: RiskLow,
			Category:  "文件",
		},
		{
			ToolID:      "FileWrite",
			TaskType:    "file.write",
			NameCN:      "写入文件",
			Description: "创建或完整覆盖本机的文本文件。高风险：会覆盖已有内容，调用前必须向用户确认目标路径与内容。",
			InputSchema: ObjSchema(map[string]interface{}{
				"path":    Str("文件绝对路径"),
				"content": Str("要写入的完整内容"),
				"append":  Bool("是否追加而非覆盖（可选，默认 false）"),
			}, "path", "content"),
			RiskLevel: RiskHigh,
			Category:  "文件",
			Confirm:   true,
		},
		{
			ToolID:      "FileDelete",
			TaskType:    "file.delete",
			NameCN:      "删除文件",
			Description: "删除本机指定的文件或目录。高风险：删除后不可恢复，调用前必须向用户确认目标路径。",
			InputSchema: ObjSchema(map[string]interface{}{
				"path":      Str("文件或目录绝对路径"),
				"recursive": Bool("目录是否递归删除（可选，默认 false）"),
			}, "path"),
			RiskLevel: RiskHigh,
			Category:  "文件",
			Confirm:   true,
		},
		{
			ToolID:      "FileMove",
			TaskType:    "file.move",
			NameCN:      "移动/重命名文件",
			Description: "移动或重命名本机文件/目录。高风险：会改变原路径，调用前必须向用户确认源与目标路径。",
			InputSchema: ObjSchema(map[string]interface{}{
				"source":      Str("源路径"),
				"destination": Str("目标路径"),
			}, "source", "destination"),
			RiskLevel: RiskHigh,
			Category:  "文件",
			Confirm:   true,
		},
		{
			ToolID:      "FileCopy",
			TaskType:    "file.copy",
			NameCN:      "复制文件",
			Description: "复制本机文件或目录。目标已存在时行为由实现决定，调用前建议确认目标路径。",
			InputSchema: ObjSchema(map[string]interface{}{
				"source":      Str("源路径"),
				"destination": Str("目标路径"),
				"overwrite":   Bool("目标存在时是否覆盖（可选）"),
			}, "source", "destination"),
			RiskLevel: RiskMedium,
			Category:  "文件",
		},
		{
			ToolID:      "FileMkdir",
			TaskType:    "file.mkdir",
			NameCN:      "创建目录",
			Description: "创建本机目录（含所需的上级目录）。",
			InputSchema: ObjSchema(map[string]interface{}{
				"path": Str("目录绝对路径"),
			}, "path"),
			RiskLevel: RiskMedium,
			Category:  "文件",
		},

		// ============ 进程（分类：进程） ============
		{
			ToolID:      "ProcessList",
			TaskType:    "process.list",
			NameCN:      "列出进程",
			Description: "列出本机运行中的进程。只读。返回进程 ID、名称、CPU 与内存占用等；可按名称过滤。",
			InputSchema: ObjSchema(map[string]interface{}{
				"filter": Str("按进程名过滤（可选）"),
				"limit":  Int("返回条数上限（可选，默认 50）"),
			}),
			RiskLevel: RiskLow,
			Category:  "进程",
		},
		{
			ToolID:      "ProcessStart",
			TaskType:    "process.start",
			NameCN:      "启动进程",
			Description: "在本机启动一个进程。高风险：可能长期占用资源，调用前必须向用户确认命令与参数。",
			InputSchema: ObjSchema(map[string]interface{}{
				"command": Str("可执行文件路径或命令"),
				"args":    Str("命令行参数（可选）"),
				"cwd":     Str("工作目录（可选）"),
			}, "command"),
			RiskLevel: RiskHigh,
			Category:  "进程",
			Confirm:   true,
		},
		{
			ToolID:      "ProcessKill",
			TaskType:    "process.kill",
			NameCN:      "终止进程",
			Description: "按进程 ID 终止本机进程。高风险：强制结束可能丢失未保存数据，调用前必须向用户确认目标进程。",
			InputSchema: ObjSchema(map[string]interface{}{
				"pid": Int("进程 ID"),
			}, "pid"),
			RiskLevel: RiskHigh,
			Category:  "进程",
			Confirm:   true,
		},
		{
			ToolID:      "ProcessKillByName",
			TaskType:    "process.kill_by_name",
			NameCN:      "按名称终止进程",
			Description: "按进程名终止本机所有同名进程。高风险：可能一次结束多个进程，调用前必须向用户确认进程名。",
			InputSchema: ObjSchema(map[string]interface{}{
				"name": Str("进程名称"),
			}, "name"),
			RiskLevel: RiskHigh,
			Category:  "进程",
			Confirm:   true,
		},

		// ============ 服务（分类：服务） ============
		{
			ToolID:      "ServiceList",
			TaskType:    "service.list",
			NameCN:      "列出系统服务",
			Description: "列出本机系统服务及其运行状态。只读。",
			InputSchema: ObjSchema(nil),
			RiskLevel:   RiskLow,
			Category:    "服务",
		},
		{
			ToolID:      "ServiceStatus",
			TaskType:    "service.status",
			NameCN:      "查询服务状态",
			Description: "查询指定系统服务的详细状态。只读。",
			InputSchema: ObjSchema(map[string]interface{}{
				"name": Str("服务名称"),
			}, "name"),
			RiskLevel: RiskLow,
			Category:  "服务",
		},
		{
			ToolID:      "ServiceStart",
			TaskType:    "service.start",
			NameCN:      "启动服务",
			Description: "启动本机指定系统服务。中风险：会改变服务运行状态。",
			InputSchema: ObjSchema(map[string]interface{}{
				"name": Str("服务名称"),
			}, "name"),
			RiskLevel: RiskMedium,
			Category:  "服务",
		},
		{
			ToolID:      "ServiceStop",
			TaskType:    "service.stop",
			NameCN:      "停止服务",
			Description: "停止本机指定系统服务。中风险：停止服务可能中断业务。",
			InputSchema: ObjSchema(map[string]interface{}{
				"name": Str("服务名称"),
			}, "name"),
			RiskLevel: RiskMedium,
			Category:  "服务",
		},

		// ============ 浏览器（分类：浏览器） ============
		{
			ToolID:      "BrowserOpen",
			TaskType:    "browser.open",
			NameCN:      "打开浏览器",
			Description: "启动受控浏览器会话并打开指定页面。返回会话标识供后续操作复用。",
			InputSchema: ObjSchema(map[string]interface{}{
				"url": Str("要打开的页面地址（可选）"),
			}),
			RiskLevel: RiskMedium,
			Category:  "浏览器",
		},
		{
			ToolID:      "BrowserClose",
			TaskType:    "browser.close",
			NameCN:      "关闭浏览器",
			Description: "关闭受控浏览器会话，释放资源。",
			InputSchema: ObjSchema(map[string]interface{}{
				"session": Str("会话标识（可选，默认当前会话）"),
			}),
			RiskLevel: RiskMedium,
			Category:  "浏览器",
		},
		{
			ToolID:      "BrowserNavigate",
			TaskType:    "browser.navigate",
			NameCN:      "浏览器跳转",
			Description: "让受控浏览器跳转到指定地址。返回页面标题与最终地址。",
			InputSchema: ObjSchema(map[string]interface{}{
				"url": Str("目标页面地址"),
			}, "url"),
			RiskLevel: RiskMedium,
			Category:  "浏览器",
		},
		{
			ToolID:      "BrowserScreenshot",
			TaskType:    "browser.screenshot",
			NameCN:      "页面截图",
			Description: "对受控浏览器当前页面截图。只读。返回截图文件路径。",
			InputSchema: ObjSchema(map[string]interface{}{
				"path": Str("截图保存路径（可选）"),
			}),
			RiskLevel: RiskLow,
			Category:  "浏览器",
		},
		{
			ToolID:      "BrowserExecute",
			TaskType:    "browser.execute",
			NameCN:      "执行页面脚本",
			Description: "在受控浏览器当前页面中执行 JavaScript 并返回结果。高风险：脚本可读取或修改页面数据，调用前必须向用户确认脚本内容。",
			InputSchema: ObjSchema(map[string]interface{}{
				"script": Str("要执行的 JavaScript 代码"),
			}, "script"),
			RiskLevel: RiskHigh,
			Category:  "浏览器",
			Confirm:   true,
		},
		{
			ToolID:      "BrowserGetCookies",
			TaskType:    "browser.get_cookies",
			NameCN:      "获取 Cookie",
			Description: "获取受控浏览器当前会话的 Cookie。只读但含敏感凭据：输出已脱敏，不得传播。",
			InputSchema: ObjSchema(nil),
			RiskLevel:   RiskLow,
			Category:    "浏览器",
		},
		{
			ToolID:      "BrowserSetCookies",
			TaskType:    "browser.set_cookies",
			NameCN:      "设置 Cookie",
			Description: "向受控浏览器写入 Cookie。中风险：会改变会话身份状态。",
			InputSchema: ObjSchema(map[string]interface{}{
				"cookies": Str("Cookie 内容（JSON 数组字符串）"),
			}, "cookies"),
			RiskLevel: RiskMedium,
			Category:  "浏览器",
		},
		{
			ToolID:      "BrowserSetHeaders",
			TaskType:    "browser.set_headers",
			NameCN:      "设置请求头",
			Description: "设置受控浏览器的额外请求头。中风险：可能影响页面请求行为。",
			InputSchema: ObjSchema(map[string]interface{}{
				"headers": Str("请求头内容（JSON 对象字符串）"),
			}, "headers"),
			RiskLevel: RiskMedium,
			Category:  "浏览器",
		},
		{
			ToolID:      "BrowserClick",
			TaskType:    "browser.click",
			NameCN:      "点击页面元素",
			Description: "在受控浏览器页面上点击指定元素。中风险：可能触发页面上的提交或删除等操作。",
			InputSchema: ObjSchema(map[string]interface{}{
				"selector": Str("元素选择器（CSS 或 XPath）"),
			}, "selector"),
			RiskLevel: RiskMedium,
			Category:  "浏览器",
		},
		{
			ToolID:      "BrowserInput",
			TaskType:    "browser.input",
			NameCN:      "输入文本",
			Description: "在受控浏览器页面的指定输入框中输入文本。中风险。",
			InputSchema: ObjSchema(map[string]interface{}{
				"selector": Str("输入框选择器"),
				"text":     Str("要输入的文本"),
				"submit":   Bool("输入后是否回车提交（可选）"),
			}, "selector", "text"),
			RiskLevel: RiskMedium,
			Category:  "浏览器",
		},
		{
			ToolID:      "BrowserWaitFor",
			TaskType:    "browser.wait_for",
			NameCN:      "等待页面元素",
			Description: "等待受控浏览器页面出现指定元素。只读。",
			InputSchema: ObjSchema(map[string]interface{}{
				"selector": Str("元素选择器"),
				"timeout":  Int("超时毫秒数（可选，默认 10000）"),
			}, "selector"),
			RiskLevel: RiskLow,
			Category:  "浏览器",
		},
	}
}

// ExecuteTask 通过本地服务既有任务处理器执行工具，并归一化结果为 MCP 结果结构。
//
// 复用 task.Registry 中已注册的处理器，因此 MCP 工具与 /api/task 走同一条
// 执行链路（含任务落库、日志记录），无需为 MCP 单独实现执行逻辑。
func ExecuteTask(tool *Tool, args map[string]interface{}) *Result {
	handler, ok := task.GetHandler(tool.TaskType)
	if !ok {
		return Fail("本地服务未注册该任务处理器: " + tool.TaskType)
	}

	params := args
	if params == nil {
		params = map[string]interface{}{}
	}

	result, err := handler.Execute(context.Background(), params)
	if err != nil {
		return Fail("执行失败: " + err.Error())
	}
	if result == nil {
		return Fail("工具未返回结果")
	}

	// 归一化：任务结果 {success, data, error} → MCP 结果 {status, msg, data}
	out := &Result{
		Status: result.Success,
		Msg:    "done",
		Data:   result.Data,
	}
	if !result.Success {
		out.Msg = result.Error
		if out.Msg == "" {
			out.Msg = "执行失败"
		}
	}
	return out
}

// MarshalJSON 便于测试与调试时输出结果
func (r *Result) MarshalJSON() ([]byte, error) {
	type alias Result
	return json.Marshal((*alias)(r))
}
