# CmsPro-本地服务-特性清单

> 版本：1.1.7 | 更新日期：2026-09-20
> 应用版本：1.1.7 | 应用 ID：cmspro.localservice

---

## 一、应用体系架构

| 特性 | 说明 | 实现位置 |
|------|------|----------|
| 目录隔离 | 应用文件全部位于 `app/Apps/CmsproLocalservice/` | `app/Apps/CmsproLocalservice/` |
| 命名空间隔离 | 使用 `App\Apps\CmsproLocalservice\` 命名空间 | 各 PHP 文件顶部 |
| 数据库隔离 | 应用表使用 `app_cmspro_localservice_` 前缀 | `Migrations/` |
| 配置隔离 | 配置键 `apps.cmspro.localservice`，数据库 code 前缀 `app_cmspro_localservice_` | `Config/localservice.php`、`manifest.json` |
| 视图隔离 | 视图命名空间 `cmspro.localservice` | `ServiceProvider::loadViews()` |
| 服务单例 | `LocalServiceClient`、`ServiceManager`、`ScriptService` 注册为单例 | `ServiceProvider::register()` |
| 安装卸载 | `Install.php` 处理安装/卸载/升级 | `Install.php` |
| 三终端 | 后台（admin） | `ServiceProvider::registerRoutes()` |
| Go 服务 | 独立常驻 Go 服务（Windows 服务/Linux daemon）承担实际执行 | `Bin/`、`go-src/` |
| AI 透传扩展 | 通用 LLM 流式透传处理器：Go 承接 SSE 双向转发（goroutine 不占 php-cgi worker），PHP 保留决策/日志/账号维护；通用路由 `/open/llm/{app}/{prefix}/*path`，任意应用按契约接入；决策契约支持 `local_response` 本地短路、`body_override` 请求体改写（决策请求随 `body` 上送原始请求体，改写权在应用侧）；连接层失败（连接失败/超时/流中断）在未输出实质数据前自动换号重试，心跳不掩蔽空闲超时 | `go-src/internal/llmrelay/`、`doc/CmsPro-本地服务-AI透传扩展指南.md` |

---

## 二、三终端全覆盖

| 终端 | 路由前缀 | 中间件 | 说明 |
|------|----------|--------|------|
| 后台管理 | `/admin/cmspro/localservice` | `web`, `auth:admin` | 仪表盘、任务、日志、拓展、指令、配置 |
| 后台 API | `/api/admin/cmspro/localservice` | `web`, `auth:admin` | 后台管理接口 |

**实现位置**：`ServiceProvider::registerRoutes()`

> 本应用无用户端（`/user/`）与前台（`/`）页面，但 `LocalServiceClient` 注册为全局单例，可被其他应用通过依赖注入引用。

---

## 三、路由系统

| 路由文件 | 说明 |
|----------|------|
| `Routes/admin.php` | 后台页面路由 |
| `Routes/admin_api.php` | 后台 API 路由 |

**实现位置**：`Routes/` 目录

---

## 四、数据层

| 表名 | 说明 |
|------|------|
| `app_cmspro_localservice_custom_scripts` | 指令库表（CustomScript） |

**模型**：`Models/CustomScript.php`

**实现位置**：`Migrations/`、`Models/`

> Go 端使用 SQLite（`storage/local_service.db`）存储任务、日志、拓展数据，由 Go 服务自动创建与初始化。

---

## 五、钩子系统

本应用未注册钩子（manifest.json 未声明 `hooks` 字段，无 `Hooks.php`）。

---

## 六、配置系统

配置组 `service_settings`（manifest.json `config_groups`），数据库 code 前缀 `app_cmspro_localservice_`：

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `port` | number | 9501 | 本地服务监听端口 |
| `auth_token` | password | 自动生成 | 认证 Token（内部 API 使用） |
| `log_level` | select | info | 日志级别（debug/info/warn/error） |
| `log_retention_days` | number | 30 | 日志保留天数 |

**实现位置**：`manifest.json`、`Config/localservice.php`、`Controllers/Admin/SettingController.php`

---

## 七、权限系统

| 权限码 | 名称 |
|--------|------|
| `cmspro.localservice.manage` | 本地服务管理 |

**实现位置**：`manifest.json` `permissions`

---

## 八、安装卸载升级

| 操作 | 行为 |
|------|------|
| 安装 | 执行迁移（创建 custom_scripts 表）、自动生成 32 位随机 Token、注册菜单/配置/权限 |
| 卸载 | 回滚迁移、清理菜单/配置/权限 |
| 升级 | 执行增量迁移 |

**实现位置**：`Install.php`

---

## 九、视图规范

| 视图 | 路径 | 说明 |
|------|------|------|
| 仪表盘 | `Views/Admin/dashboard/index.blade.php` | 独立完整 HTML 页面 |
| 任务记录 | `Views/Admin/tasks/index.blade.php` | 独立完整 HTML 页面 |
| 日志查看 | `Views/Admin/logs/index.blade.php` | 独立完整 HTML 页面 |
| 拓展管理 | `Views/Admin/extensions/index.blade.php` | 独立完整 HTML 页面 |
| 指令管理 | `Views/Admin/scripts/index.blade.php` | 独立完整 HTML 页面 |
| 基本配置 | `Views/Admin/settings/index.blade.php` | 独立完整 HTML 页面 |

**实现位置**：`Views/` 目录

---

## 十、菜单系统

| 终端 | 菜单 | 路径 |
|------|------|------|
| 后台 | 本地服务（父） | - |
| 后台 | ├ 仪表盘 | `/admin/cmspro/localservice/dashboard` |
| 后台 | ├ 任务记录 | `/admin/cmspro/localservice/tasks` |
| 后台 | ├ 日志查看 | `/admin/cmspro/localservice/logs` |
| 后台 | ├ 拓展管理 | `/admin/cmspro/localservice/extensions` |
| 后台 | ├ 指令管理 | `/admin/cmspro/localservice/scripts` |
| 后台 | └ 基本配置 | `/admin/cmspro/localservice/settings` |

**实现位置**：`manifest.json` `menus`

---

## 十一、版本历史

| 版本 | 日期 | 说明 |
|------|------|------|
| 1.0.0 | 2026-08-13 | 初始版本，基于应用 v1.0.1 编写 |
| 1.1.0 | 2026-09-18 | 新增 AI 透传扩展（llmrelay）：通用 LLM SSE 透传端点 `/open/llm/*`、决策/日志回调契约、`cmspro_base_url` 配置项；基于应用 v1.1.0 |
| 1.1.1 | 2026-09-18 | 透传契约新增 `body_override` 请求体改写与 `local_response` 本地短路能力 |
| 1.1.2 | 2026-09-18 | Go 故障无感切换增强：连接层失败与心跳后失败可换号重试；心跳不掩蔽空闲超时（修复半开连接永不判定）；基于应用 v1.1.2 |
| 1.1.3 | 2026-09-18 | PHP 侧 disable_functions 兼容修复：架构检测改用 php_uname()、shell 调用统一安全封装（ServiceManager::safeShellExec）；基于应用 v1.1.3 |
| 1.1.4 | 2026-09-18 | Linux 安装/卸载脚本兼容 systemd 目录 immutable 加固（宝塔系统加固场景）：自动检测并临时解除 `/etc/systemd/system` 及既有 unit 文件的 `chattr +i`，操作完成后恢复；基于应用 v1.1.4 |
| 1.1.6 | 2026-09-20 | 透传超时语义修正：retry_policy 契约新增 `header_timeout_ms`（等响应头预算，0=回退 first_byte_timeout_ms 兜底）；`first_byte_timeout_ms` 回归真实语义（响应头后首数据空读上限，与 read_timeout_ms 取较小值），修复大上下文 prefill 期间「http2: timeout awaiting response headers」误判；基于应用 v1.1.6 |
| 1.1.7 | 2026-09-20 | 透传兼容性补齐：① 零输出判定——上游 200 后未产出实质内容 chunk（content/reasoning/tool_calls/finish_reason 均无）即关闭判 `failed` 可换号重试，首个实质 chunk 前数据行仅缓冲不输出；② SSE 非标准字段过滤——剔除顶层 `provider`/`native_finish_reason`/`service_tier` 与 choices 内 `native_finish_reason`（对齐 PHP 通道 sanitizeChunk），干净行原始字节透传；③ 错误 chunk 兼容格式——错误 data 行同时携带 `choices`（空 delta + finish_reason stop，对齐 PHP formatSseError）；④ 决策请求 `body` 以原始字节上送、应用侧对象模式解码保真嵌套空对象（契约说明补充）；基于应用 v1.1.7 |
