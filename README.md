# 本地服务 应用文档

> 应用ID：`cmspro.localservice` | 文档版本：1.0.0 | 应用版本：1.1.4 | 依赖：PHP ≥ 8.1, CmsPro ≥ 5.0.0

- 官方地址：https://www.cmspro.cn/apps/cmspro.localservice
- 本应用是 CmsPro v5 的本地系统服务应用，基于 CMSPRO 运行，请先安装 [CMSPRO](https://www.cmspro.cn/help/2)

## 1. 应用概述

CmsPro 本地系统服务：以「PHP 管理面 + Go 执行面」双层架构提供浏览器自动化、进程管理、文件操作等系统级能力。

- **后台管理端**：仪表盘（服务状态/启停控制）、任务记录、日志查看、拓展管理、指令管理、基本配置（端口/认证Token/日志级别/保留天数）
- **Go 常驻服务**：Windows 服务 / Linux daemon 独立进程承担实际执行（预编译二进制随应用分发，源码位于 `go-src/`），使用 SQLite 本地存储任务、日志、拓展数据
- **指令库**：`custom_scripts` 表维护自定义指令，按平台（Windows/Linux）区分，供 Go 侧执行
- **全局单例复用**：`LocalServiceClient`、`ServiceManager`、`ScriptService` 注册为容器单例，其他应用可通过依赖注入引用
- **AI 透传扩展（1.1.0）**：通用 LLM 流式透传处理器——Go 承接 SSE 双向转发（goroutine 不占 php-cgi worker，解决本地并行 AI 对话响应慢问题），PHP 保留鉴权决策/选号/日志/账号维护；通用路由 `/open/llm/{app}/{prefix}/*path` 支持任意应用按契约接入（首个接入方：CmsproClineproxy）

## 2. 目录结构

```
CmsproLocalservice/
├── Bin/                                # 预编译 Go 服务二进制（windows/linux）与运行存储
│   ├── windows/cmspro-local-service.exe
│   ├── linux/                          # linux amd64 / arm64 二进制
│   └── storage/                        # 运行配置 local_service_config.json + SQLite 数据
├── Config/localservice.php             # 默认配置值
├── Controllers/Admin/                  # 后台控制器（仪表盘/任务/日志/拓展/指令/设置，Api/ 子目录为管理接口）
├── Migrations/                         # 应用表迁移（custom_scripts 建表/加字段/加配置项）
├── Models/CustomScript.php             # 指令库模型
├── Routes/
│   ├── admin.php                       # 后台页面路由
│   └── admin_api.php                   # 后台管理接口路由
├── Services/
│   ├── LocalServiceClient.php          # PHP → Go 本地服务 HTTP 客户端（单例）
│   ├── ScriptService.php               # 指令业务逻辑（单例）
│   └── ServiceManager.php              # 服务安装/启停/配置同步管理（单例）
├── Views/Admin/                        # 后台视图（dashboard/tasks/logs/extensions/scripts/settings）
├── go-src/                             # Go 服务源码
│   ├── cmd/service/                    # 程序入口
│   ├── internal/
│   │   ├── api/                        # HTTP 路由注册（含 /open/llm 透传路由）
│   │   ├── config/                     # 运行配置加载与保存
│   │   ├── extension/                  # 拓展管理
│   │   ├── llmrelay/                   # AI 通用透传处理器（1.1.0 新增）
│   │   ├── storage/                    # SQLite 本地存储
│   │   ├── task/                       # 任务执行引擎
│   │   └── winservice/                 # Windows 服务封装
│   ├── build.ps1                       # 编译脚本（产物输出至 Bin/windows）
│   └── go.mod / go.sum                 # Go 模块定义
├── Install.php                         # 安装/卸载/升级逻辑
├── ServiceProvider.php                 # 应用服务提供者（路由/视图/单例注册）
├── manifest.json                       # 应用元数据唯一声明源
├── icon.svg                            # 应用图标
└── doc/                                # 应用文档
```

## 3. 应用文档

| 文档 | 说明 |
|------|------|
| [CmsPro-本地服务-特性清单.md](doc/CmsPro-本地服务-特性清单.md) | 应用特性与对应实现位置 |
| [CmsPro-本地服务-使用指南.md](doc/CmsPro-本地服务-使用指南.md) | 安装、配置、服务管理指南 |
| [CmsPro-本地服务应用扩展指南.md](doc/CmsPro-本地服务应用扩展指南.md) | 服务能力调用等扩展接口说明 |
| [CmsPro-本地服务-AI透传扩展指南.md](doc/CmsPro-本地服务-AI透传扩展指南.md) | AI 通用透传处理器接入契约（local_response 短路、决策/日志回调） |
