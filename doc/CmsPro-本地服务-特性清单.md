# CmsPro-本地服务-特性清单

> 版本：1.1.0 | 更新日期：2026-09-18
> 应用版本：1.1.0 | 应用 ID：cmspro.localservice

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
| AI 透传扩展 | 通用 LLM 流式透传处理器：Go 承接 SSE 双向转发（goroutine 不占 php-cgi worker），PHP 保留决策/日志/账号维护；通用路由 `/open/llm/{app}/{prefix}/*path`，任意应用按契约接入 | `go-src/internal/llmrelay/`、`doc/CmsPro-本地服务-AI透传扩展指南.md` |

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
