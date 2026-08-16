// internal/task/file.go
package task

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"os"
)

type FileReadHandler struct{}

func (h *FileReadHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("缺少 path 参数")
	}

	encoding, _ := params["encoding"].(string)
	if encoding == "" {
		encoding = "utf8"
	}

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	if encoding == "base64" {
		return &TaskResult{
			Success: true,
			Data: map[string]interface{}{
				"content":  base64.StdEncoding.EncodeToString(data),
				"encoding": "base64",
				"size":     len(data),
			},
		}, nil
	}

	return &TaskResult{
		Success: true,
		Data: map[string]interface{}{
			"content":  string(data),
			"encoding": "utf8",
			"size":     len(data),
		},
	}, nil
}

type FileWriteHandler struct{}

func (h *FileWriteHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("缺少 path 参数")
	}

	content, _ := params["content"].(string)
	appendMode := false
	if a, ok := params["append"].(bool); ok {
		appendMode = a
	}

	flag := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	f, err := os.OpenFile(path, flag, 0644)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	_, err = f.WriteString(content)
	if err != nil {
		return nil, fmt.Errorf("写入文件失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"written": len(content)},
	}, nil
}

type FileExistsHandler struct{}

func (h *FileExistsHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("缺少 path 参数")
	}

	_, err := os.Stat(path)
	exists := err == nil

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"exists": exists},
	}, nil
}

type FileListHandler struct{}

func (h *FileListHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	dir, _ := params["path"].(string)
	if dir == "" {
		return nil, fmt.Errorf("缺少 path 参数")
	}

	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}

	var items []map[string]interface{}
	for _, entry := range entries {
		items = append(items, map[string]interface{}{
			"name":     entry.Name(),
			"is_dir":   entry.IsDir(),
			"size":     entry.Size(),
			"mod_time": entry.ModTime().Format("2006-01-02 15:04:05"),
		})
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"items": items},
	}, nil
}

type FileDeleteHandler struct{}

func (h *FileDeleteHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("缺少 path 参数")
	}

	err := os.RemoveAll(path)
	if err != nil {
		return nil, fmt.Errorf("删除失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"deleted": true},
	}, nil
}

type FileMkdirHandler struct{}

func (h *FileMkdirHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("缺少 path 参数")
	}

	err := os.MkdirAll(path, 0755)
	if err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"created": true},
	}, nil
}

func init() {
	Register("file.read", &FileReadHandler{})
	Register("file.write", &FileWriteHandler{})
	Register("file.exists", &FileExistsHandler{})
	Register("file.list", &FileListHandler{})
	Register("file.delete", &FileDeleteHandler{})
	Register("file.mkdir", &FileMkdirHandler{})
	Register("file.move", &FileMoveHandler{})
	Register("file.copy", &FileCopyHandler{})
	Register("file.info", &FileInfoHandler{})
}

// FileMoveHandler 移动/重命名文件或目录
type FileMoveHandler struct{}

func (h *FileMoveHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	src, _ := params["src"].(string)
	dst, _ := params["dst"].(string)
	if src == "" || dst == "" {
		return nil, fmt.Errorf("缺少 src 或 dst 参数")
	}

	if err := os.Rename(src, dst); err != nil {
		return nil, fmt.Errorf("移动失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"moved": true, "src": src, "dst": dst},
	}, nil
}

// FileCopyHandler 复制文件（仅支持单个文件）
type FileCopyHandler struct{}

func (h *FileCopyHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	src, _ := params["src"].(string)
	dst, _ := params["dst"].(string)
	if src == "" || dst == "" {
		return nil, fmt.Errorf("缺少 src 或 dst 参数")
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("源文件不存在: %w", err)
	}
	if srcInfo.IsDir() {
		return nil, fmt.Errorf("暂不支持复制目录: %s", src)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("打开源文件失败: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return nil, fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer dstFile.Close()

	written, err := io.Copy(dstFile, srcFile)
	if err != nil {
		return nil, fmt.Errorf("复制失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data:    map[string]interface{}{"copied": written, "src": src, "dst": dst},
	}, nil
}

// FileInfoHandler 获取文件/目录元信息
type FileInfoHandler struct{}

func (h *FileInfoHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("缺少 path 参数")
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TaskResult{
				Success: true,
				Data:    map[string]interface{}{"exists": false},
			}, nil
		}
		return nil, fmt.Errorf("获取文件信息失败: %w", err)
	}

	return &TaskResult{
		Success: true,
		Data: map[string]interface{}{
			"exists":   true,
			"name":     info.Name(),
			"is_dir":   info.IsDir(),
			"size":     info.Size(),
			"mode":     info.Mode().String(),
			"mod_time": info.ModTime().Format("2006-01-02 15:04:05"),
		},
	}, nil
}
