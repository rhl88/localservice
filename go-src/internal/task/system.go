// internal/task/system.go
package task

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

type SystemExecHandler struct{}

func (h *SystemExecHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("缺少 command 参数")
	}

	timeoutSec := 30
	if t, ok := params["timeout"].(float64); ok && t > 0 {
		timeoutSec = int(t)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}

	output, err := cmd.CombinedOutput()
	// Windows 终端输出为 GBK 编码，转为 UTF-8
	if runtime.GOOS == "windows" {
		decoded, decodeErr := io.ReadAll(transform.NewReader(bytes.NewReader(output), simplifiedchinese.GBK.NewDecoder()))
		if decodeErr == nil {
			output = decoded
		}
	}
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("命令执行失败: %w", err)
		}
	}

	return &TaskResult{
		Success: true,
		Data: map[string]interface{}{
			"stdout":    string(output),
			"exit_code": exitCode,
		},
	}, nil
}

type SystemInfoHandler struct{}

func (h *SystemInfoHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	return &TaskResult{
		Success: true,
		Data: map[string]interface{}{
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"cpus":       runtime.NumCPU(),
			"go_version": runtime.Version(),
		},
	}, nil
}

func init() {
	Register("system.exec", &SystemExecHandler{})
	Register("system.info", &SystemInfoHandler{})
	Register("system.script", &SystemScriptHandler{})
	Register("system.network", &SystemNetworkHandler{})
}

// SystemScriptHandler 执行多行脚本，根据 type 选择解释器（bat/ps1/sh/python）
type SystemScriptHandler struct{}

func (h *SystemScriptHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	script, _ := params["script"].(string)
	if script == "" {
		return nil, fmt.Errorf("缺少 script 参数")
	}

	scriptType, _ := params["type"].(string)
	if scriptType == "" {
		scriptType = "bat"
	}

	timeoutSec := 60
	if t, ok := params["timeout"].(float64); ok && t > 0 {
		timeoutSec = int(t)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// 按脚本类型选择文件扩展名与解释器
	var ext, interpreter string
	var interpArgs []string
	switch scriptType {
	case "bat":
		if runtime.GOOS == "windows" {
			ext = ".bat"
			interpreter = "cmd"
			interpArgs = []string{"/C"}
		} else {
			// Linux 无 cmd，降级用 sh 执行（兼容简单批处理）
			ext = ".sh"
			interpreter = "sh"
		}
	case "ps1":
		ext = ".ps1"
		if runtime.GOOS == "windows" {
			interpreter = "powershell"
			interpArgs = []string{"-ExecutionPolicy", "Bypass", "-NoProfile", "-File"}
		} else {
			interpreter = "pwsh"
			interpArgs = []string{"-ExecutionPolicy", "Bypass", "-NoProfile", "-File"}
		}
	case "sh":
		ext = ".sh"
		interpreter = "sh"
	case "python":
		ext = ".py"
		if runtime.GOOS == "windows" {
			interpreter = "python"
		} else {
			interpreter = "python3"
		}
	default:
		return nil, fmt.Errorf("不支持的脚本类型: %s", scriptType)
	}

	// 按类型处理脚本文件字节编码
	// - ps1: 加 UTF-8 BOM，PowerShell 5.x 按 BOM 识别编码
	// - bat (Windows): 移除 chcp 65001（与 GBK 转码冲突），UTF-8 转 GBK
	// - sh/python: 直接写入 UTF-8
	var scriptBytes []byte
	switch scriptType {
	case "ps1":
		scriptBytes = append([]byte{0xEF, 0xBB, 0xBF}, []byte(script)...)
	case "bat":
		if runtime.GOOS == "windows" {
			// 移除 chcp 65001 行：Go 端已将内容转为 GBK，chcp 会导致后续中文解析错乱
			cleaned := regexp.MustCompile(`(?im)^chcp\s+65001[^\n]*\n?`).ReplaceAllString(script, "")
			// 统一换行符为 \r\n（cmd.exe 期望 CRLF，纯 \n 会导致命令被错误拼接）
			cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
			cleaned = strings.ReplaceAll(cleaned, "\n", "\r\n")
			// 逐字符转 GBK，无法转换的字符（如 emoji）替换为 ?
			scriptBytes = gbkEncode(cleaned)
		} else {
			scriptBytes = []byte(script)
		}
	default:
		scriptBytes = []byte(script)
	}

	scriptPath := filepath.Join(os.TempDir(), "cmspro_script_"+uuid.New().String()+ext)
	if err := os.WriteFile(scriptPath, scriptBytes, 0o700); err != nil {
		return nil, fmt.Errorf("写入脚本文件失败: %w", err)
	}
	defer os.Remove(scriptPath)

	args := append(interpArgs, scriptPath)
	cmd := exec.CommandContext(ctx, interpreter, args...)

	output, err := cmd.CombinedOutput()
	// 编码转换：bat/ps1 在 Windows 下默认输出 GBK；其它情况按 UTF-8 检测
	// 用 utf8.Valid 判断是否已是 UTF-8，避免对 UTF-8 内容误转
	if runtime.GOOS == "windows" && (scriptType == "bat" || scriptType == "ps1") && !utf8.Valid(output) {
		decoded, decodeErr := io.ReadAll(transform.NewReader(bytes.NewReader(output), simplifiedchinese.GBK.NewDecoder()))
		if decodeErr == nil {
			output = decoded
		}
	}
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("脚本执行失败: %w", err)
		}
	}

	return &TaskResult{
		Success: true,
		Data: map[string]interface{}{
			"stdout":    string(output),
			"exit_code": exitCode,
		},
	}, nil
}

// gbkEncode 逐字符将 UTF-8 字符串转为 GBK 字节，无法转换的字符替换为 '?'
// 用于 .bat 文件写入：emoji 等字符无法用 GBK 表示，整段转换会失败导致内容截断
func gbkEncode(src string) []byte {
	encoder := simplifiedchinese.GBK.NewEncoder()
	var result bytes.Buffer
	for _, r := range src {
		b, err := encoder.Bytes([]byte(string(r)))
		if err != nil || len(b) == 0 {
			result.WriteByte('?')
		} else {
			result.Write(b)
		}
	}
	return result.Bytes()
}

// SystemNetworkHandler 返回本机网络接口信息
type SystemNetworkHandler struct{}

func (h *SystemNetworkHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("获取网络接口失败: %w", err)
	}

	var result []map[string]interface{}
	for _, iface := range ifaces {
		var addrs []string
		if addrList, err := iface.Addrs(); err == nil {
			for _, addr := range addrList {
				addrs = append(addrs, addr.String())
			}
		}
		result = append(result, map[string]interface{}{
			"name":       iface.Name,
			"mac":        iface.HardwareAddr.String(),
			"mtu":        iface.MTU,
			"up":         iface.Flags&net.FlagUp != 0,
			"addresses":  addrs,
		})
	}

	hostname, _ := os.Hostname()
	return &TaskResult{
		Success: true,
		Data: map[string]interface{}{
			"hostname":   hostname,
			"interfaces": result,
		},
	}, nil
}
