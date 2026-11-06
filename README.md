# 本地服务 应用文档

> 应用ID：`cmspro.localservice` | 文档版本：1.1.0 | 应用版本：1.4.0 | 依赖：PHP ≥ 8.1, CmsPro ≥ 5.0.0

- 官方地址：https://www.cmspro.cn/apps/cmspro.localservice
- 本应用是 CmsPro v5 的本地系统服务应用，基于 CMSPRO 运行，请先安装 [CMSPRO](https://www.cmspro.cn/help/2)

## 1. 应用概述

以「PHP 管理面 + Go 执行面」双层架构，为 CmsPro 提供浏览器自动化、进程管理、文件操作等系统级能力：PHP 侧保留页面、接口、权限与配置，Go 常驻服务（Windows 服务 / Linux daemon）承担实际执行，并使用 SQLite 本地存储任务、日志与拓展数据。

- **后台管理端**：仪表盘（服务状态/启停/在线更新/最近任务）、任务记录、日志查看、拓展管理、指令管理、基本配置（端口/认证 Token/日志级别/保留天数/时区/开放 API）、MCP 服务
- **在线更新**：下载 → 替换 → 校验分段状态机，替换后服务未按期上线自动回滚旧版本；PHP 禁用 `exec`/`shell_exec` 或运行权限不足时自动降级为经 Go 服务执行，并在设置页给出手动命令清单
- **MCP 服务**：把本机 32 个工具（系统/文件/进程/服务/浏览器）按 MCP 2025-03-26 Streamable HTTP 开放给外部 AI 工具；工具按低/中/高三级风险分级（12/11/9），低风险默认授权、中高风险 opt-in；每凭据独立「凭据 × 工具」授权矩阵；IP 白名单（`*`/单 IP/CIDR/区间）+ 滑动窗口限流 + 随机路由前缀（`/mcp-{12位}`）+ 调用审计；高风险调用需二次确认（确认键含凭据 + 工具 + 完整参数哈希，TTL 600 秒、一次性消费）
- **AI 透传扩展**：Go 承接 SSE 双向转发（goroutine 流式转发不占 php-cgi worker），PHP 保留鉴权决策/选号/日志/账号维护；通用路由 `/open/llm/{app}/{prefix}/*path` 支持任意应用按契约接入，决策契约支持 `local_response` 本地短路、`body_override` 请求体改写、`responses_to_chat` 响应协议转换（Responses SSE → Chat Completions SSE）；连接层失败（连接失败/超时/流中断）在未输出实质数据前自动换号重试
- **桌面小工具**：向 WebOS 桌面提供「本地服务状态」1x1 卡片，在线 `running`、离线但二进制存在 `stopped`、二进制缺失 `warning`，展示端口/累计任务/成功率
- **全局单例复用**：`LocalServiceClient`、`ServiceManager`、`ScriptService`、`UpdateService` 注册为容器单例，其他应用可通过依赖注入引用
- **权限控制**：`cmspro.localservice.manage` 统一管控后台管理入口与接口

## 2. 应用截图

> 截图来源：[官方应用页 · cmspro.localservice](https://www.cmspro.cn/apps/cmspro.localservice)（官网截图版本 v1.0.0，应用页当前版本 v1.3.0）。每次发布与更新必须核对本节截图与官网「应用截图」是否一致，不一致时以官网为准同步更新。

![应用截图 1](https://www.cmspro.cn/uploads/2026/07/03/1mVdrKuO6CIrGzu3rBmU1zIx0TCoNMUNceLlMwno.png)

![应用截图 2](https://www.cmspro.cn/uploads/2026/07/03/cO7xJbpLoEqnopTDpIX5CVYIFXVhk4AFmCa2KjT8.png)

![应用截图 3](https://www.cmspro.cn/uploads/2026/07/03/J1CQYC5iZ9EgLEbXcUacEOiRSWqi1fChOrm185TL.png)

![应用截图 4](https://www.cmspro.cn/uploads/2026/07/03/6cJSHXicDunxvzc7eXcgHI2ba4UaGaT3azNaSUw0.png)

![应用截图 5](https://www.cmspro.cn/uploads/2026/07/03/izCoSpl3SLqqsA6iTtZjDr1juHoHwT0TREInBehs.png)

![应用截图 6](https://www.cmspro.cn/uploads/2026/07/03/f0nbSYvifceqn4FgTu5wyuBgBC8oA2q2C1U39Tno.png)

## 3. 目录结构

```
CmsproLocalservice/
├── Bin/
│   ├── windows/                        # Windows 预编译二进制（按需构建/下载）
│   ├── linux/                          # Linux amd64 / arm64 二进制
│   └── storage/                        # 运行配置 local_service_config.json + SQLite 数据（运行时生成，不入库）
├── Config/localservice.php             # 默认配置值
├── Controllers/Admin/                  # 后台控制器（仪表盘/任务/日志/拓展/指令/设置）
│   └── Api/                            # 后台管理接口（含 MCP 管理端点）
├── Migrations/                         # 应用表迁移（app_cmspro_localservice_ 前缀）
├── Models/
│   ├── CustomScript.php                # 指令库模型
│   └── McpKey.php                      # MCP 访问凭据模型
├── Routes/
│   ├── admin.php                       # 后台页面路由
│   └── admin_api.php                   # 后台管理接口路由
├── Services/
│   ├── LocalServiceClient.php          # PHP → Go 本地服务 HTTP 客户端
│   ├── ScriptService.php               # 指令业务逻辑
│   ├── ServiceManager.php              # 服务安装/启停/配置同步管理
│   ├── UpdateService.php               # 在线更新状态机
│   └── McpService.php                  # MCP 凭据与访问控制
├── Views/Admin/                        # 后台视图（dashboard/tasks/logs/extensions/scripts/settings/mcp）
├── Tests/                              # 应用功能测试（Feature/）
├── go-src/                             # Go 服务源码
│   ├── cmd/service/                    # 程序入口
│   ├── internal/
│   │   ├── api/                        # HTTP 路由注册（含 /open/llm 透传路由）
│   │   ├── buildinfo/                  # 构建版本信息
│   │   ├── config/                     # 运行配置加载与保存
│   │   ├── extension/                  # 拓展管理
│   │   ├── llmrelay/                   # AI 透传处理器与协议转换
│   │   ├── mcp/                        # MCP 服务端
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

## 4. 应用文档

| 文档 | 说明 |
|------|------|
| [CmsPro-本地服务-特性清单.md](doc/CmsPro-本地服务-特性清单.md) | 应用特性与对应实现位置 |
| [CmsPro-本地服务-使用指南.md](doc/CmsPro-本地服务-使用指南.md) | 安装、配置、服务管理与在线更新指南 |
| [CmsPro-本地服务应用扩展指南.md](doc/CmsPro-本地服务应用扩展指南.md) | 服务能力调用等扩展接口说明 |
| [CmsPro-本地服务-AI透传扩展指南.md](doc/CmsPro-本地服务-AI透传扩展指南.md) | AI 通用透传接入契约（local_response / body_override / responses_to_chat） |
| [CmsPro-本地服务-MCP服务指南.md](doc/CmsPro-本地服务-MCP服务指南.md) | MCP 服务接入、凭据授权、工具分级与审计说明 |

## 5. 安装与验证

通过 CmsPro 应用管理安装后，进入「本地服务 → 仪表盘」生成安装脚本，以管理员（Windows）或 root（Linux）权限运行完成服务安装；服务随系统启动，可在设置页调整端口、认证 Token 与日志策略。

```bash
# 在 code 目录执行（应用级测试单独运行，不纳入框架回归）
php artisan test app/Apps/CmsproLocalservice/Tests

# 结果：61 passed (248 assertions)
```