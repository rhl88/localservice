//go:build windows

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
	"cmspro-local-service/internal/storage"
	"cmspro-local-service/internal/task"
	"cmspro-local-service/internal/winservice"
)

// Version 程序版本号
const Version = "1.0.0"

func main() {
	// SCM 启动时先检查是否为 Windows 服务模式
	if winservice.IsWindowsService() {
		svc := winservice.NewService(runForeground)
		if err := svc.Run(); err != nil {
			log.Fatalf("服务运行失败: %v", err)
		}
		return
	}

	if len(os.Args) < 2 {
		fmt.Println("Usage: cmspro-local-service [install|uninstall|start|stop|status|run|service]")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		// 由 sc start 或手动启动，自动检测服务模式
		if winservice.IsWindowsService() {
			svc := winservice.NewService(runForeground)
			if err := svc.Run(); err != nil {
				log.Fatalf("服务运行失败: %v", err)
			}
		} else {
			runForeground()
		}
	case "service":
		// Windows 服务模式（手动调试入口）
		svc := winservice.NewService(runForeground)
		if err := svc.Run(); err != nil {
			log.Fatalf("服务运行失败: %v", err)
		}
	case "install":
		installService()
	case "uninstall":
		uninstallService()
	case "start":
		startService()
	case "stop":
		stopService()
	case "status":
		statusService()
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

// getConfigPath 返回配置文件路径
// 优先从可执行文件所在目录向上查找 storage 目录，降级使用当前目录
func getConfigPath() string {
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

	// 从数据库加载已保存的拓展配置，覆盖内存中的默认值
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
		log.Printf("加载数据库拓展配置失败: %v，使用默认拓展", err)
	}

	// 初始化任务管理器
	taskMgr := task.NewManager(db)

	// 启动日志自动清理定时器（每 24 小时清理一次超过保留天数的日志）
	startLogCleaner(db, cfg.LogRetentionDays)

	// 初始化处理器并启动 HTTP 服务器
	handlers := api.NewHandlers(db, taskMgr, extMgr)
	server := api.NewServer(handlers)
	if err := server.Start(); err != nil {
		log.Fatalf("HTTP 服务器启动失败: %v", err)
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

// installService 安装 Windows 服务
func installService() {
	exePath, err := winservice.GetExePath()
	if err != nil {
		fmt.Printf("获取路径失败: %v\n", err)
		os.Exit(1)
	}
	if err := winservice.Install(exePath); err != nil {
		fmt.Printf("安装失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("服务安装成功")
}

// uninstallService 卸载 Windows 服务
func uninstallService() {
	if err := winservice.Uninstall(); err != nil {
		fmt.Printf("卸载失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("服务卸载成功")
}

// startService 启动 Windows 服务
func startService() {
	if err := winservice.Start(); err != nil {
		fmt.Printf("启动失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("服务已启动")
}

// stopService 停止 Windows 服务
func stopService() {
	if err := winservice.Stop(); err != nil {
		fmt.Printf("停止失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("服务已停止")
}

// statusService 查询服务状态
func statusService() {
	status, err := winservice.Status()
	if err != nil {
		fmt.Printf("查询失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("服务状态: %s\n", status)
}
