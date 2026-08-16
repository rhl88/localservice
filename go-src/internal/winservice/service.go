//go:build windows

// Package winservice 提供 CmsPro 本地服务的 Windows 服务封装。
// 支持服务的安装、卸载、启动、停止、状态查询，以及作为 Windows 服务运行。
package winservice

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// ServiceName 服务标识名（SCM 中的服务名）
const ServiceName = "CmsProLocalService"

// ServiceDisplayName 服务显示名
const ServiceDisplayName = "CmsPro 本地服务"

// ServiceDescription 服务描述
const ServiceDescription = "CmsPro 本地桌面服务，提供浏览器自动化、系统命令、文件操作等能力"

// Service 封装 Windows 服务运行逻辑
type Service struct {
	runFunc func()
}

// NewService 创建服务实例，runFunc 为服务主逻辑（前台运行函数）
func NewService(runFunc func()) *Service {
	return &Service{runFunc: runFunc}
}

// Execute 实现 svc.Handler 接口，由 SCM 在服务模式下调用
func (s *Service) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// 启动主逻辑
	go s.runFunc()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				log.Println("服务正在停止...")
				break loop
			default:
				log.Printf("未支持的控制命令: %v\n", c.Cmd)
			}
		}
	}

	changes <- svc.Status{State: svc.StopPending}
	return false, 0
}

// Run 以 Windows 服务模式运行（由 SCM 调用）
func (s *Service) Run() error {
	return svc.Run(ServiceName, s)
}

// IsWindowsService 判断当前进程是否由 Windows SCM 作为服务启动
func IsWindowsService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return isService
}

// Install 安装 Windows 服务，exePath 为可执行文件绝对路径
func Install(exePath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("服务已存在")
	}

	s, err = m.CreateService(ServiceName, exePath, mgr.Config{
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return err
	}
	defer s.Close()

	// 安装事件日志（忽略已存在错误）
	if err := eventlog.InstallAsEventCreate(ServiceName, eventlog.Info|eventlog.Warning|eventlog.Error); err != nil {
		log.Printf("安装事件日志失败（忽略）: %v", err)
	}

	return nil
}

// Uninstall 卸载 Windows 服务
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("服务不存在")
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return err
	}

	eventlog.Remove(ServiceName)
	return nil
}

// Start 启动已安装的 Windows 服务
func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("服务未安装")
	}
	defer s.Close()

	return s.Start()
}

// Stop 停止 Windows 服务
func Stop() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("服务未安装")
	}
	defer s.Close()

	_, err = s.Control(svc.Stop)
	return err
}

// Status 查询服务状态，返回 running/stopped/starting/stopping/not_installed/unknown
func Status() (string, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", err
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return "not_installed", nil
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return "unknown", err
	}

	switch status.State {
	case svc.Running:
		return "running", nil
	case svc.Stopped:
		return "stopped", nil
	case svc.StartPending:
		return "starting", nil
	case svc.StopPending:
		return "stopping", nil
	default:
		return "unknown", nil
	}
}

// GetExePath 返回当前可执行文件的绝对路径
func GetExePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(exePath)
	if err != nil {
		return "", err
	}
	return absPath, nil
}
