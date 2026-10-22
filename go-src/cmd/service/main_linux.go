//go:build !windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"cmspro-local-service/internal/api"
	"cmspro-local-service/internal/config"
	"cmspro-local-service/internal/extension"
	"cmspro-local-service/internal/mcp"
	"cmspro-local-service/internal/storage"
	"cmspro-local-service/internal/task"
)

// Version 程序版本号
const Version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: cmspro-local-service [run]")
		fmt.Println("  run\t前台运行模式（Linux 平台唯一支持的命令）")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runForeground()
	default:
		fmt.Printf("命令 '%s' 仅在 Windows 平台支持\n", os.Args[1])
		os.Exit(1)
	}
}

// getConfigPath 返回配置文件路径
// 优先从环境变量 LOCAL_SERVICE_CONFIG_PATH 读取，否则从可执行文件所在目录
// 向上查找 storage 目录，降级使用当前目录。
func getConfigPath() string {
	if p := os.Getenv("LOCAL_SERVICE_CONFIG_PATH"); p != "" {
		return p
	}
	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		for i := 0; i < 5; i++ {
			if _, err := os.Stat(filepath.Join(dir, "storage")); err == nil {
				return filepath.Join(dir, "storage", "local_service_config.json")
			}
			dir = filepath.Dir(dir)
		}
	}
	return "local_service_config.json"
}

// runForeground 前台运行模式，启动 HTTP 服务器
func runForeground() {
	fmt.Printf("CmsPro LocalService v%s starting in foreground mode...\n", Version)

	configPath := getConfigPath()
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	log.Printf("配置文件: %s", configPath)
	log.Printf("监听端口: %s", cfg.Port)

	// 设置全局时区，影响 time.Now() 及所有 time.Local 引用
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			log.Printf("警告: 无效的时区配置 %q，使用系统默认时区: %v", cfg.Timezone, err)
		} else {
			time.Local = loc
			log.Printf("时区: %s", cfg.Timezone)
		}
	}

	// 初始化数据库（时区由配置决定，影响所有时间字段的生成）
	dbPath := cfg.DBPath
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(filepath.Dir(configPath), "..", dbPath)
	}
	db, err := storage.NewDB(dbPath, cfg.Timezone)
	if err != nil {
		log.Fatalf("初始化数据库失败: %v", err)
	}
	defer db.Close()
	log.Printf("数据库: %s", dbPath)

	// 初始化拓展管理器
	extMgr := extension.NewManager()
	task.SetExtensionManager(extMgr)

	// 从数据库加载已保存的拓展配置
	// 同时确保默认拓展在数据库中存在，避免新建数据库后列表为空
	extension.SyncDefaultsToDB(db, extMgr)
	dbExts, err := db.ListExtensions()
	if err == nil {
		extRecords := make([]*extension.Extension, 0, len(dbExts))
		for _, e := range dbExts {
			path := ""
			config := ""
			if e.Path != nil {
				path = *e.Path
			}
			if e.Config != nil {
				config = *e.Config
			}
			extRecords = append(extRecords, &extension.Extension{
				Name:   e.Name,
				Title:  e.Title,
				Type:   e.Type,
				Path:   path,
				Config: config,
				Status: e.Status,
			})
		}
		extMgr.SyncFromDB(extRecords)
		log.Printf("从数据库加载了 %d 个拓展配置", len(extRecords))
	} else {
		log.Printf("加载数据库拓展配置失败: %v，使用默认拓展（如有）", err)
	}

	// 初始化任务管理器
	taskMgr := task.NewManager(db)

	// 启动日志自动清理定时器（每 24 小时清理一次超过保留天数的日志）
	startLogCleaner(db, cfg.LogRetentionDays)

	// 初始化处理器并启动 HTTP 服务器
	handlers := api.NewHandlers(db, taskMgr, extMgr)

	// 装配 MCP 服务（配置由 PHP 后台写入本配置文件后下发）
	mcpServer := mcp.NewServerFrom(buildMcpConfig(cfg), Version, mcp.NewStorageAuditSink(func(level, message string) error {
		appID := "cmspro.localservice"
		return db.InsertLog(appID, nil, level, message, nil)
	}), nil)
	handlers.SetMcp(mcpServer)

	// MCP 配置热重载：PHP 后台变更下发后经 /api/mcp/reload 触发，免重启生效
	handlers.SetMcpReloader(func() error {
		fresh, err := config.Load(getConfigPath())
		if err != nil {
			return err
		}
		mcpServer.Reload(buildMcpConfig(fresh))
		return nil
	})

	server := api.NewServer(handlers)
	if err := server.Start(); err != nil {
		log.Fatalf("HTTP 服务器启动失败: %v", err)
	}
}

// buildMcpConfig 把配置文件的 MCP 段映射为 MCP 引导配置（未配置时返回禁用态）
func buildMcpConfig(cfg *config.Config) *mcp.BootstrapConfig {
	if cfg == nil || cfg.MCP == nil {
		return &mcp.BootstrapConfig{Enabled: false}
	}
	src := cfg.MCP
	creds := make([]*mcp.Credential, 0, len(src.Credentials))
	for _, c := range src.Credentials {
		creds = append(creds, &mcp.Credential{
			ID:          c.ID,
			Token:       c.Token,
			Name:        c.Name,
			ToolIDs:     c.ToolIDs,
			Disabled:    c.Disabled,
			CreatedAt:   c.CreatedAt,
			ExpiresDays: c.ExpiresDays,
			Active:      c.Active,
		})
	}
	return &mcp.BootstrapConfig{
		Enabled:         src.Enabled,
		PathPrefix:      src.PathPrefix,
		IPAllowlist:     src.IPAllowlist,
		RateLimitMax:    src.RateLimitMax,
		RateLimitWindow: src.RateLimitWindow,
		DefaultTimeout:  src.DefaultTimeout,
		MaxConcurrent:   src.MaxConcurrent,
		Credentials:     creds,
	}
}

// startLogCleaner 启动后台日志清理定时器
// 启动时立即清理一次，之后每 24 小时清理一次超过 retentionDays 的日志
func startLogCleaner(db *storage.DB, retentionDays int) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	go func() {
		clean := func() {
			if err := db.CleanOldLogs(retentionDays); err != nil {
				log.Printf("日志清理失败: %v", err)
			}
		}
		clean()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			clean()
		}
	}()
	log.Printf("日志自动清理已启动，保留天数: %d", retentionDays)
}
