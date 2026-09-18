# CmsPro-本地服务-使用指南

> 版本：1.0.0 | 更新日期：2026-09-18
> 应用版本：1.1.3 | 应用 ID：cmspro.localservice

---

## 一、环境要求

| 项目 | 要求 |
|------|------|
| PHP | >= 8.1 |
| CmsPro | >= 5.0.0 |
| 操作系统 | Windows 或 Linux |
| 浏览器 | Chrome/Chromium（浏览器自动化任务依赖，路径自动检测） |
| Go | 1.20+（仅在重新编译 Go 二进制时需要，运行环境无需安装） |

> 本应用无其他 CmsPro 应用依赖，但它是其他应用（如抖音下载、工作流）的底层能力提供者。

**PHP `disable_functions` 兼容性说明**：宝塔等面板常禁用 `shell_exec`/`exec`。本应用所有 shell 调用均经安全检测封装——函数被禁时不再报 500，界面按钮返回明确的业务提示（如「请通过生成的服务脚本手动操作」）；生成安装/卸载脚本、二进制架构检测等核心链路改用 `php_uname()` 纯 PHP 实现，完全不受该限制影响。

---

## 二、安装

### 2.1 安装应用

在 CmsPro 后台「应用管理」中安装本应用，或通过 Artisan 命令：

```bash
cd /path/to/cmspro/code
php artisan app:install cmspro.localservice
```

安装过程自动执行迁移，创建以下数据表：

| 表名 | 说明 |
|------|------|
| `app_cmspro_localservice_custom_scripts` | 指令库表 |

同时自动生成 32 位随机 Token 写入 `config_items`（code 为 `app_cmspro_localservice_auth_token`）。

### 2.2 启用应用

在后台「应用 → 已装应用」中找到「本地服务」，点击「启用」。

### 2.3 权限配置

应用声明以下权限，需在角色管理中分配给管理员：

| 权限码 | 名称 |
|--------|------|
| `cmspro.localservice.manage` | 本地服务管理 |

---

## 三、功能验证

### 3.1 注册系统服务

应用会在 `storage/app/localservice/` 目录生成安装/卸载脚本。

**Windows 平台**：右键以管理员身份运行生成的 `.bat` 脚本，完成服务注册并启动。

**Linux 平台**：以 root 身份运行生成的 `.sh` 脚本，完成 systemd 服务注册并启动。

### 3.2 后台功能验证

后台菜单入口「本地服务」，含以下子模块：

| 菜单 | 路径 | 验证要点 |
|------|------|----------|
| 仪表盘 | `/admin/cmspro/localservice/dashboard` | 服务状态、任务统计 |
| 任务记录 | `/admin/cmspro/localservice/tasks` | 任务列表（按 app_id 过滤） |
| 日志查看 | `/admin/cmspro/localservice/logs` | 日志列表（按 level 过滤） |
| 拓展管理 | `/admin/cmspro/localservice/extensions` | 拓展列表、配置、测试 |
| 指令管理 | `/admin/cmspro/localservice/scripts` | 指令库 CRUD、执行 |
| 基本配置 | `/admin/cmspro/localservice/settings` | 端口、Token、日志级别、开放 API |

### 3.3 服务状态验证

在「仪表盘」页面查看服务状态，确认 Go 服务在线（HTTP 可达且 code=0）。

### 3.4 命令行管理

Go 二进制支持以下子命令（需管理员权限）：

| 命令 | 说明 |
|------|------|
| `install` | 注册 Windows 服务 |
| `uninstall` | 卸载 Windows 服务 |
| `start` | 启动已注册的服务 |
| `stop` | 停止服务 |
| `status` | 查询服务状态 |
| `run` | 前台运行模式（调试用） |
| `service` | 显式以 Windows 服务模式运行 |

---

## 四、卸载验证

1. 在后台「应用 → 已装应用」中找到「本地服务」，点击「卸载」。
2. 系统自动回滚数据表（custom_scripts）、清理菜单/配置/权限。
3. 卸载后验证：后台菜单消失、相关数据表已删除。

> 卸载会删除指令库数据，如需保留请先备份数据库。Go 服务需通过命令行 `uninstall` 单独卸载。

---

## 五、目录结构

```
CmsproLocalservice/
├── Config/localservice.php        # 应用配置
├── Bin/                           # Go 编译产物
│   ├── storage/                   # SQLite 预置数据库与配置
│   ├── windows/                   # Windows x86_64 编译产物
│   └── linux/                     # Linux x86_64 / ARM64 编译产物
├── Controllers/Admin/             # 后台控制器（含 Api/ 子目录）
├── Migrations/                    # 数据库迁移
├── Models/CustomScript.php        # 指令库模型
├── Routes/                        # admin.php / admin_api.php
├── Services/                      # LocalServiceClient、ServiceManager、ScriptService
├── Views/Admin/                   # 后台视图
├── go-src/                        # Go 服务源码
├── Install.php                    # 安装/卸载/升级
├── ServiceProvider.php            # 服务提供者
└── manifest.json                  # 应用清单
```

---

## 六、常见问题

### Q1：服务启动后 HTTP 探测不通？

- 确认 Windows 服务状态为 `running`（`sc query CmsProLocalService`）或 Linux systemd 状态为 `active`
- 确认端口未被占用（`netstat -ano | findstr :9501` 或 `ss -tlnp | grep 9501`）
- 确认 `storage/local_service_config.json`（以及 `Bin/storage/local_service_config.json`）中端口与 PHP 端配置一致

### Q2：开放 API 调用返回 403？

- 在「基本配置」页面检查开放 API 是否已启用
- 确认 `X-API-Key` 请求头已携带且与配置页显示的密钥一致
- 修改密钥后需重启 Go 服务生效
- 确认请求路径前缀为 `/api/open/`（非 `/api/`）

### Q3：浏览器任务报「Chrome 拓展未启用」？

- 在「拓展管理」页面将 chrome 拓展状态改为启用，或通过 API 启用

### Q4：Windows 命令输出乱码？

- `system.exec` 已做 GBK→UTF-8 转码。若仍乱码，检查脚本是否通过 `system.script` 执行（该任务类型未做转码，需在 PHP 端用 `ScriptService::execute()` 调用）

### Q5：PHP 端调用一直返回 503？

- 确认 Go 服务在运行（`isOnline()` 返回 false 即服务未启动）
- 确认 Token 一致：PHP 端 `config_items` 与 Go 端 `local_service_config.json` 中的 `auth_token` 必须相同
- 修改 Token 或端口后，需在「基本配置」页面保存触发 `syncConfig()`，并重启 Go 服务

---

## 七、版本历史

| 版本 | 日期 | 说明 |
|------|------|------|
| 1.0.0 | 2026-08-13 | 初始版本，基于应用 v1.0.1 编写 |
| 1.1.0 | 2026-09-18 | 新增 AI 透传扩展（llmrelay）：Go 服务提供通用 LLM SSE 透传端点 `/open/llm/{app}/{prefix}/*path` 与健康检查 `/open/llm/ping`，Go 侧新增 `cmspro_base_url` 配置（回调 CmsPro 基础地址，随 `syncConfig()` 同步）；决策契约支持 `local_response` 本地短路（管理类端点如 models 由 CmsPro 侧本地出数，不透传上游）；接入契约与使用方法见《CmsPro-本地服务-AI透传扩展指南》 |
| 1.1.1 | 2026-09-18 | 透传契约新增 `body_override` 请求体改写：决策请求随 `body` 上送客户端原始请求体，决策响应下发 `body_override` 时 Go 整体替换原始请求体转发上游（如应用侧模型别名解析为真实 model_id）；换号重试时决策始终基于原始请求体，改写不污染后续决策 |
| 1.1.2 | 2026-09-18 | Go 透传故障无感切换增强：上游连接失败、首字节超时、空闲超时、流中断等连接层失败在未向客户端输出实质数据前自动换号重试（此前直接终止）；心跳注释不再重置空闲计时，修复上游挂死时半开连接永不判定导致客户端长时间收不到数据自行断开的问题 |
| 1.1.3 | 2026-09-18 | PHP 侧 disable_functions 兼容修复：宝塔等面板禁用 shell_exec 时生成安装/卸载脚本报 500 的问题——二进制架构检测改用 php_uname() 纯 PHP 实现；服务启停等 shell 调用统一经 function_exists 安全封装，函数被禁时返回明确业务提示而非致命错误 |
