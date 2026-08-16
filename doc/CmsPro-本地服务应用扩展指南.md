# CmsPro 本地服务应用扩展指南

> 版本：1.2.0 | 更新日期：2026-08-12
> 适用系统：CmsPro v5.0.0+
> 应用 ID：`cmspro.localservice`

***

## 一、应用概述

CmsPro 本地服务（CmsproLocalservice）为 CmsPro 提供系统级能力的本地底座。它由一个用 Go 编译的 Windows 服务（`cmspro-local-service.exe`）承担实际执行，PHP 端通过 HTTP 调用并将能力以服务容器形式暴露给其他应用。

### 设计动机

PHP-FPM 环境受限于 FastCGI 进程模型，无法直接启动后台进程、操作 Windows 服务或长时间运行浏览器自动化任务。本应用通过独立常驻的 Go 服务承担这些"超能力"职责，PHP 端仅做 HTTP 转发与结果封装，既不阻塞 Web 请求，也不破坏现有架构边界。

### 核心特性

- **常驻服务**：Go 二进制注册为 Windows 服务（`CmsProLocalService`），开机自启、独立运行
- **任务模型**：同步执行（`sync`）立即返回结果，异步执行（`async`）返回 `task_id` 后台运行
- **能力清单**：系统命令、文件操作、进程管理、服务管理、浏览器自动化（基于 chromedp）
- **拓展管理**：内置 Chrome 浏览器拓展（默认启用），预留 FFmpeg、Python 等拓展位
- **审计完整**：所有任务和日志写入 SQLite，按 `app_id` 维度隔离，支持分页查询与自动清理
- **统一客户端**：`LocalServiceClient` 通过服务容器注入，任意 CmsPro 应用均可调用
- **认证隔离**：内部 API 使用 Bearer Token 鉴权（安装时自动生成 32 位随机 Token），开放 API 使用 X-API-Key 鉴权（可在设置页面启用并配置）
- **开放 API**：支持第三方跨服务器调用，通过 `X-API-Key` 请求头认证，可在基本设置中启用/关闭并重新生成密钥

### 架构总览

```
┌──────────────────────────────────────────────────────────────┐
│  CmsPro 应用层（PHP）                                          │
│  ┌─────────────────────┐  ┌──────────────────────────────┐  │
│  │ 其他应用（如 DouyinDown）│  │ CmsproLocalservice 后台界面    │  │
│  │ app(LocalServiceClient)│  │ 仪表盘/任务/日志/拓展/指令/配置 │  │
│  └──────────┬──────────┘  └──────────────┬───────────────┘  │
│             │                            │                    │
│             └────────────┬───────────────┘                    │
│                          ▼                                    │
│           ┌──────────────────────────────────────┐           │
│           │ LocalServiceClient（单例）             │           │
│           │ Http::withToken($token) 或 X-API-Key  │           │
│           └──────────────┬───────────────────────┘           │
└──────────────────────────┼───────────────────────────────────┘
                           │ HTTP 127.0.0.1:9501
                           ▼
┌──────────────────────────────────────────────────────────────┐
│  Go 服务（cmspro-local-service.exe，Windows 服务/Linux daemon）│
│  ┌──────────┐ ┌───────────┐ ┌──────────────────┐ ┌────────┐│
│  │ Bearer   │ │ OpenAPI   │ │ Task Manager     │ │Ext.    ││
│  │ 路由(/api)│ │路由(/api/ │ │                  │ │Manager ││
│  │          │ │open/)     │ │                  │ │        ││
│  └────┬─────┘ └─────┬─────┘ └────────┬─────────┘ └───┬────┘│
│       │              │               │                 │     │
│   ┌───┴──────────────┴───────────────┴─────────────────┴──┐ │
│   │ 任务处理器（task.Register 注册）                       │ │
│   │ file.*  system.*  process.*  browser.*  service.*     │ │
│   └───────────────────────────────────────────────────────┘ │
│                          │                                    │
│                          ▼                                    │
│           ┌──────────────────────────────┐                   │
│           │ SQLite（storage/local_service.db） │              │
│           │ tasks / logs / extensions       │                │
│           └──────────────────────────────┘                   │
└──────────────────────────────────────────────────────────────┘
```

### 依赖

| 依赖 | 说明 |
|------|------|
| Go 1.20+ | 仅在重新编译 Go 二进制时需要，运行环境无需安装 |
| Chrome/Chromium 浏览器 | 浏览器自动化任务依赖，安装 Chrome 即可，路径自动检测 |
| SQLite | Go 二进制内置驱动（`modernc.org/sqlite`），无需额外安装 |
| PHP >= 8.1 | CmsPro v5 框架要求 |
| `golang.org/x/sys/windows` | Windows 服务封装依赖 |

***

## 二、安装与部署

### 2.1 安装应用

通过 CmsPro 后台「应用市场」安装本地服务应用。安装脚本会：

1. 执行迁移，创建指令库表 `app_cmspro_localservice_custom_scripts`
2. 自动生成 32 位随机 Token 写入 `config_items`（code 为 `app_cmspro_localservice_auth_token`）

> 应用本身不依赖任何 Composer 包，所有外部能力由 Go 二进制承载。

### 2.2 注册系统服务

应用会在 `storage/app/localservice/` 目录生成安装/卸载脚本（使用安装脚本时自动同步配置至 `storage/local_service_config.json` 和 `Bin/storage/`）。

**Windows 平台：**
右键以管理员身份运行生成的 `.bat` 脚本，完成服务注册并启动。

脚本内容大致为（由 `ServiceManager::installScript()` 生成）：

```bat
"<二进制路径>" install
sc start "CmsProLocalService"
```

**Linux 平台：**
以 root 身份运行生成的 `.sh` 脚本，完成 systemd 服务注册并启动。

脚本内容大致为：

```sh
# 复制二进制
cp "<二进制路径>" /usr/local/bin/cmspro-local-service
# 创建 systemd 单元
cat > /etc/systemd/system/cmspro-local-service.service << 'EOF'
[Unit]
Description=CmsPro 本地服务
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/cmspro-local-service run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable cmspro-local-service
systemctl start cmspro-local-service
```

### 2.3 命令行管理

Go 二进制支持以下子命令（需管理员权限）：

| 命令 | 说明 |
|------|------|
| `install` | 注册 Windows 服务（自动启动类型） |
| `uninstall` | 卸载 Windows 服务 |
| `start` | 启动已注册的服务 |
| `stop` | 停止服务 |
| `status` | 查询服务状态（running/stopped/not_installed 等） |
| `run` | 前台运行模式（调试用，不注册服务） |
| `service` | 显式以 Windows 服务模式运行（通常由 SCM 自动拉起） |

无参数运行时，二进制会自动检测是否由 SCM 拉起，是则进入服务模式，否则打印用法。

### 2.4 配置文件

Go 服务从 `storage/local_service_config.json` 加载配置，PHP 端在以下时机会同步写入该文件：

- 生成安装/卸载脚本时
- 保存「基本配置」时（通过 `ServiceManager::syncConfig()`）

文件格式：

```json
{
    "port": "9501",
    "auth_token": "32位随机字符串",
    "log_level": "info",
    "log_retention_days": 30
}
```

配置文件查找规则：从可执行文件所在目录向上最多查找 5 层，定位到 `storage` 目录后读取 `local_service_config.json`。

> **注意**：`Bin/storage/` 目录下预置了 `local_service.db`（SQLite 数据库），因此 `getConfigPath()` 在第 2 级向上查找时会先匹配到 `Bin/storage/`。为确保 PHP 端配置同步后 Go 服务能读取最新配置，`ServiceManager::syncConfig()` 会**同时写入** `storage/local_service_config.json` 和 `Bin/storage/local_service_config.json` 两个路径。

文件不存在时使用默认配置启动，不视为错误。

### 2.5 后台配置项

「后台 → 本地服务 → 基本配置」可调整以下配置：

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| 监听端口 | number | 9501 | Go 服务监听端口，修改后需重启服务 |
| 认证 Token | password | 自动生成 | Bearer Token（内部 API 使用），留空安装时自动生成，可在配置页重新生成 |
| 日志级别 | select | info | 可选 debug/info/warn/error |
| 日志保留天数 | number | 30 | 超过天数的日志由 Go 服务每 24 小时自动清理一次 |
| 开放 API | switch | 关闭 | 启用后第三方可通过 X-API-Key 调用 Go 服务，路由前缀 `/api/open/` |
| 开放 API 密钥 | text | 自动生成 | 启用开放 API 时需设置的密钥，可在配置页重新生成 |

> **重要**：修改端口或 Token 后，必须重启 Go 服务才能生效。Token 重新生成后，所有调用方（其他应用）会自动使用新 Token，因为 `LocalServiceClient` 每次请求都从 `config_items` 读取。开放 API 密钥修改后同样需重启服务。

***

## 三、核心服务 API

### 3.1 获取客户端实例

`LocalServiceClient` 注册为单例，任意应用通过服务容器注入即可：

```php
use App\Apps\CmsproLocalservice\Services\LocalServiceClient;

$client = app(LocalServiceClient::class);
```

客户端内部完成 Token 注入、超时控制、错误兜底，调用方无需关心 HTTP 细节。

### 3.2 任务执行

#### 同步执行

阻塞等待结果，适合短耗时任务（命令执行、文件读取等）：

```php
$result = $client->exec('cmspro.douyindown', 'system.exec', [
    'command' => 'dir C:\\',
    'timeout' => 10,
]);

// $result 结构：['code' => 0, 'message' => 'success', 'data' => [...]]
if ($result['code'] === 0) {
    echo $result['data']['stdout'];
}
```

#### 异步执行

立即返回 `task_id`，任务在 Go 服务后台运行，适合长耗时任务（浏览器自动化、批量脚本）：

```php
$taskId = $client->execAsync('cmspro.douyindown', 'browser.screenshot', [
    'browser_id' => $bid,
    'selector'    => '#qrcode',
]);

if ($taskId === null) {
    // 提交失败
}
```

#### 查询任务状态

```php
$status = $client->getTaskStatus($taskId);
// $status['data'] 包含 status: pending/running/success/failed/cancelled
//                以及 result、error、duration_ms 等字段
```

#### 取消任务

```php
$client->cancelTask($taskId);
// 仅对运行中（running）的异步任务有效
```

### 3.3 服务状态

```php
// 服务是否在线（HTTP 可达且 code=0）
if ($client->isOnline()) {
    // ...
}

// 详细状态
$status = $client->status();
// $status['data'] = ['version' => '1.0.0', 'status' => 'running']
```

### 3.4 任务/日志/拓展查询

```php
// 任务列表（按 app_id 过滤，分页）
$client->listTasks(['app_id' => 'cmspro.douyindown', 'page' => 1, 'page_size' => 20]);

// 日志列表（按 level 过滤）
$client->listLogs(['level' => 'error', 'page' => 1, 'page_size' => 50]);

// 拓展列表
$client->listExtensions();

// 拓展详情
$client->getExtension('chrome');

// 更新拓展配置
$client->updateExtension('chrome', [
    'path'   => 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    'config' => '{"headless":true,"default_timeout":30}',
    'status' => 1,
]);

// 测试拓展可用性
$client->testExtension('chrome');
```

### 3.5 统一响应结构

Go 端所有接口返回统一结构：

```json
{
    "code": 0,
    "message": "success",
    "data": { ... }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| code | int | 0 表示成功，非 0 表示失败 |
| message | string | 状态描述，失败时为错误信息 |
| data | mixed | 业务数据，失败时通常为 null |

`LocalServiceClient` 会将网络异常统一转为 `code=503` 的错误结构，**绝不抛出异常到调用方**。调用方只需判断 `code === 0` 即可。

### 3.6 app_id 约定

`app_id` 用于任务和日志的审计隔离，调用方必须传入自身应用 ID（如 `cmspro.douyindown`、`cmspro.flow`）。Go 端在 `tasks` 和 `logs` 表中持久化 `app_id` 字段，后台「任务记录」「日志查看」页面可按 `app_id` 过滤。

应用自身发起的管理操作使用 `cmspro.localservice` 作为 `app_id`。

***

## 四、任务类型参考

所有任务通过 `exec()` 或 `execAsync()` 调用，`type` 参数取值如下。

### 4.1 系统命令（system.*）

#### system.exec — 执行单行命令

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| command | string | 是 | 命令内容，Windows 走 `cmd /C`，Linux 走 `sh -c` |
| timeout | number | 否 | 超时秒数，默认 30 |

返回 `data`：

```json
{
    "stdout": "命令输出（已转 UTF-8）",
    "exit_code": 0
}
```

> Windows 下命令输出由 Go 端从 GBK 解码为 UTF-8，调用方无需二次处理。

#### system.script — 执行多行脚本

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| script | string | 是 | 脚本内容，Windows 写入 `.bat` 执行，Linux 写入 `.sh` 执行 |
| timeout | number | 否 | 超时秒数，默认 60 |

返回结构同 `system.exec`。

> **已知差异**：`system.script` 的 stdout 在 Windows 下未做 GBK→UTF-8 转码。PHP 端 `ScriptService::execute()` 已针对此场景做了双重编码修复，直接调用 `exec('system.script')` 时需自行处理编码。

#### system.info — 系统信息

无参数。返回操作系统、CPU 架构、CPU 核数、Go 版本等。

#### system.network — 网络接口信息

无参数。返回本机 hostname 与所有网络接口（名称、MAC、MTU、IP 地址列表）。

### 4.2 文件操作（file.*）

所有文件操作均要求 `path` 参数使用绝对路径。

| 任务类型 | 必填参数 | 可选参数 | 返回字段 |
|---------|---------|---------|---------|
| file.read | path | encoding（utf8/base64，默认 utf8） | content、encoding、size |
| file.write | path、content | append（bool，默认 false，覆盖写） | written（写入字节数） |
| file.exists | path | — | exists（bool） |
| file.list | path | — | items（含 name、is_dir、size、mod_time） |
| file.delete | path | — | deleted（true） |
| file.mkdir | path | — | created（true） |
| file.move | src、dst | — | moved（true） |
| file.copy | src、dst | — | copied（字节数），暂不支持复制目录 |
| file.info | path | — | exists、name、is_dir、size、mode、mod_time |

`file.read` 的 `encoding=base64` 适合读取二进制文件（图片、PDF 等），返回 base64 字符串。

### 4.3 进程管理（process.*）

| 任务类型 | 必填参数 | 可选参数 | 返回字段 |
|---------|---------|---------|---------|
| process.list | — | filter（按名称过滤） | processes（raw 行）、count |
| process.start | executable | args（参数数组） | pid |
| process.kill | pid | — | killed（true）、pid |
| process.kill_by_name | name | — | killed（bool）、name、output |

`process.list` 在 Windows 走 `tasklist /FO CSV /NH`，Linux 走 `ps aux`。`process.kill_by_name` 在 Windows 走 `taskkill /F /IM`，未匹配到进程时返回 `killed: false` 而非错误。

### 4.4 服务管理（service.*）

仅 Windows 平台支持，其他平台返回错误。

| 任务类型 | 必填参数 | 返回字段 |
|---------|---------|---------|
| service.list | — | services（含 name、state）、count |
| service.status | name | name、state（RUNNING/STOPPED/...） |
| service.start | name | name、started（true） |
| service.stop | name | name、stopped（true） |

`service.list` 走 `sc query type= service state= all`，`service.status` 走 `sc query <name>`。

### 4.5 浏览器自动化（browser.*）

基于 chromedp 实现，依赖 Chrome 拓展（默认启用）。所有浏览器任务通过 `browser_id` 关联到具体浏览器实例。

#### browser.open — 打开浏览器实例

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| headless | bool | 否 | 是否无头模式，默认 true |

返回 `browser_id`（UUID）。后续所有浏览器操作都需要此 ID。

#### browser.navigate — 导航到 URL

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| browser_id | string | 是 | 浏览器实例 ID |
| url | string | 是 | 目标 URL |
| wait_seconds | number | 否 | 导航后等待秒数 |

返回 url 和页面 title。

#### browser.screenshot — 截图

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| browser_id | string | 是 | 浏览器实例 ID |
| selector | string | 否 | CSS 选择器，指定时截取该元素，否则截整页 |

返回 base64 编码的 PNG 图片和 mime_type。

#### browser.execute — 执行 JavaScript

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| browser_id | string | 是 | 浏览器实例 ID |
| script | string | 是 | JavaScript 代码 |

返回 JS 执行结果。

#### browser.click — 点击元素

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| browser_id | string | 是 | 浏览器实例 ID |
| selector | string | 是 | CSS 选择器 |

#### browser.input — 输入文本

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| browser_id | string | 是 | 浏览器实例 ID |
| selector | string | 是 | CSS 选择器 |
| value | string | 是 | 输入内容 |

#### browser.wait_for — 等待元素可见

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| browser_id | string | 是 | 浏览器实例 ID |
| selector | string | 是 | CSS 选择器 |
| timeout | number | 否 | 超时秒数，默认 30 |

#### browser.get_cookies / browser.set_cookies — Cookie 操作

`browser.get_cookies` 仅需 `browser_id`，返回当前页面所有 Cookie（name、value、domain、path）。

`browser.set_cookies` 参数：

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| browser_id | string | 是 | 浏览器实例 ID |
| cookies | array | 是 | Cookie 对象数组，每项含 name、value、domain（可选）、path（可选） |

返回设置的 Cookie 数量。

#### browser.close — 关闭浏览器实例

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| browser_id | string | 是 | 浏览器实例 ID |

> **资源回收**：浏览器实例不会自动关闭，调用方使用完毕后**必须**调用 `browser.close` 释放 Chrome 进程，否则会累积僵尸进程。

### 4.6 完整任务类型清单

| 类别 | 任务类型 |
|------|---------|
| 文件 | file.read、file.write、file.exists、file.list、file.delete、file.mkdir、file.move、file.copy、file.info |
| 系统 | system.exec、system.script、system.info、system.network |
| 进程 | process.list、process.start、process.kill、process.kill_by_name |
| 服务 | service.list、service.status、service.start、service.stop |
| 浏览器 | browser.open、browser.navigate、browser.screenshot、browser.execute、browser.click、browser.input、browser.wait_for、browser.get_cookies、browser.set_cookies、browser.close |

***

## 五、拓展管理

### 5.1 内置拓展

Go 服务启动时通过 `initDefaultExtensions()` 注册以下拓展：

| 名称 | 标题 | 类型 | 默认状态 | 说明 |
|------|------|------|---------|------|
| chrome | Chrome 浏览器 | browser | 启用 | 浏览器自动化的底层依赖，路径自动检测 |
| ffmpeg | FFmpeg | media | 禁用 | 预留，需手动配置路径后启用 |
| python | Python | script | 禁用 | 预留，需手动配置路径后启用 |

### 5.2 Chrome 拓展配置

Chrome 拓展支持以下配置项（JSON 格式存入 `config` 字段）：

```json
{
    "headless": true,
    "user_agent": "",
    "proxy": "",
    "default_timeout": 30,
    "extra_args": ["--disable-gpu", "--no-sandbox"]
}
```

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| headless | bool | true | 是否无头模式 |
| user_agent | string | 空 | 自定义 UA，留空使用浏览器默认 |
| proxy | string | 空 | HTTP 代理，如 `http://127.0.0.1:7890` |
| default_timeout | int | 30 | 默认超时秒数 |
| extra_args | string[] | [] | 传递给 Chrome 的额外启动参数 |

### 5.3 路径自动检测

Chrome 路径检测顺序：

1. `C:\Program Files\Google\Chrome\Application\chrome.exe`
2. `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`
3. `%LOCALAPPDATA%\Google\Chrome\Application\chrome.exe`
4. Microsoft Edge 路径（作为降级方案）

未检测到时，可在「拓展管理」页面手动填写路径，或通过后台 API 安装：

```
POST /api/admin/cmspro/localservice/extensions/chrome/install
Body: { "path": "C:\\Path\\To\\chrome.exe" }
```

### 5.4 拓展测试

「拓展管理」页面对每个拓展提供「测试」按钮，对 browser 类型会启动一个临时 Chrome 实例验证可用性，对其他类型仅校验路径是否存在。测试结果不影响拓展状态。

***

## 六、指令库（CustomScript）

指令库是 PHP 端维护的自定义脚本集合，存于 MySQL 的 `app_cmspro_localservice_custom_scripts` 表，可通过后台「指令管理」页面维护，也可由其他应用调用执行。

### 6.1 数据表结构

| 字段 | 类型 | 说明 |
|------|------|------|
| id | bigint | 主键 |
| name | varchar(100) | 脚本标识，与 platform 联合唯一，如 `db_backup` |
| title | varchar(200) | 脚本名称，如「数据库备份」 |
| type | varchar(20) | 脚本类型：bat/ps1/sh/python |
| platform | varchar(20) | 适用平台：windows/linux/all |
| content | text | 脚本内容，支持 `{{key}}` 参数占位符 |
| description | text | 脚本说明 |
| params | text | 参数说明，JSON 格式 |
| status | tinyint | 1=启用，0=禁用 |
| created_at / updated_at | datetime | 时间戳 |

> **联合唯一约束**：`(name, platform)` 联合索引保证同一平台下 name 唯一。不同平台可以有相同的 name（如 Windows 和 Linux 可各有一个名称为 `install-python` 的脚本）。

### 6.2 参数占位符

脚本内容中可使用 `{{key}}` 或 `{% key %}` 形式的占位符，执行时由 `ScriptService::execute()` 替换为实际值：

```bat
:: backup.bat
@echo off
mysqldump -u{{user}} -p{{password}} {{database}} > {{output}}
```

```php
$result = $service->execute($scriptId, [
    'user'     => 'root',
    'password' => 'secret',
    'database' => 'cmspro',
    'output'   => 'D:\\backup.sql',
]);
```

### 6.3 通过 ScriptService 调用

```php
use App\Apps\CmsproLocalservice\Services\ScriptService;

$service = app(ScriptService::class);

// 执行脚本（内部走 system.script 任务）
$result = $service->execute($scriptId, ['arg1' => 'value1']);
// 返回 ['code' => 0, 'message' => 'success', 'data' => ['stdout' => ..., 'exit_code' => 0]]

// 分页查询指令库
$paginated = $service->paginate([
    'keyword'   => 'backup',
    'type'      => 'bat',
    'status'    => 1,
    'page'      => 1,
    'page_size' => 20,
]);

// 获取所有启用指令（用于下拉选择）
$enabled = $service->allEnabled();
```

> **编码修复**：`ScriptService::execute()` 已对 Windows 下 cmd 脚本的 GBK 输出做双重编码修复（Latin-1 → GBK → UTF-8），调用方拿到的 stdout 可直接使用。

### 6.4 脚本类型与平台说明

| 类型 | 执行方式 | 适用场景 |
|------|---------|---------|
| bat | Windows `cmd /C` | Windows 批处理脚本 |
| ps1 | 通过 `system.script` 执行（需 PowerShell 在 PATH） | Windows PowerShell 脚本 |
| sh | Linux `sh` | Linux Shell 脚本 |
| python | 通过 `system.script` 执行（需 Python 拓展启用） | Python 脚本 |

**平台匹配规则**（`ScriptService::execute()` 执行时）：
- `platform=windows`：仅当前系统为 Windows 时可执行
- `platform=linux`：仅当前系统为 Linux 时可执行
- `platform=all`：任何平台均可执行
- 不匹配时返回 `code=422`，提示「当前操作系统不匹配」

> 当前 `system.script` 在 Windows 下统一以 `.bat` 执行，ps1/python 类型需要脚本内容自行调用对应解释器，或直接通过 `system.exec` 调用。

***

## 七、后台 API 参考

后台 API 路由前缀 `/api/admin/cmspro/localservice`，需 `auth:admin` 中间件鉴权。所有响应使用 `ApiResponse` 统一格式。

### 7.1 服务控制

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /service/status | 服务状态与累计统计（任务总数、成功数、成功率） |
| GET | /service/installed-apps | 已安装应用列表（供前端选择 app_id） |
| POST | /service/install-script | 生成安装脚本，返回脚本路径 |
| POST | /service/uninstall-script | 生成卸载脚本 |
| POST | /service/start | 启动服务（PHP 无权限时返回 503） |
| POST | /service/stop | 停止服务 |
| POST | /service/restart | 重启服务（Windows 走 stop+start 组合，Linux 走 systemctl restart） |

### 7.2 设置

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /settings | 读取配置（端口、Token、日志级别、保留天数、开放 API 开关、开放 API 密钥） |
| POST | /settings | 保存配置（端口、日志级别、保留天数、开放 API 开关、开放 API 密钥） |
| POST | /settings/regenerate-token | 重新生成 32 位内部 API Token |
| POST | /settings/regenerate-open-api-key | 重新生成 32 位开放 API 密钥 |

### 7.3 任务

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /tasks | 任务列表，支持 app_id、status、page、page_size 查询参数 |
| GET | /tasks/{id} | 任务详情 |
| POST | /tasks/{id}/cancel | 取消运行中的任务 |
| POST | /tasks/clear | 清空任务记录 |

### 7.4 日志

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /logs | 日志列表，支持 app_id、level、page、page_size 查询参数 |
| POST | /logs/clear | 清空日志记录 |

### 7.5 拓展

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /extensions | 拓展列表 |
| GET | /extensions/{name} | 拓展详情 |
| PUT | /extensions/{name} | 更新拓展配置（path、config、status） |
| POST | /extensions/{name}/test | 测试拓展可用性 |
| POST | /extensions/{name}/install | 安装拓展（自动检测路径或接受手动 path 参数） |
| POST | /extensions/{name}/install-by-command | 通过命令安装拓展（异步任务） |
| GET | /extensions/{name}/default-install-command | 获取拓展默认安装命令 |
| GET | /extensions/{name}/install-task/{taskId} | 查询拓展安装任务结果 |

### 7.6 指令库

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /scripts | 分页列表，支持 keyword、type、platform、status |
| GET | /scripts/all | 全部启用指令（下拉选择用） |
| GET | /scripts/{id} | 指令详情 |
| POST | /scripts | 创建指令 |
| PUT | /scripts/{id} | 更新指令 |
| DELETE | /scripts/{id} | 删除指令 |
| POST | /scripts/{id}/exec | 执行指令，body 传 `{"args": {"key": "value"}}` |
| POST | /scripts/init-defaults | 初始化默认指令库 |

### 7.7 后台页面路由

页面路由前缀 `/admin/cmspro/localservice`，同样需 `auth:admin`：

| 路径 | 说明 |
|------|------|
| /dashboard | 仪表盘 |
| /tasks | 任务记录 |
| /logs | 日志查看 |
| /extensions | 拓展管理 |
| /scripts | 指令管理 |
| /settings | 基本配置 |

***

## 八、Go 服务 API 参考

Go 服务直接暴露的 HTTP API，监听 `127.0.0.1:9501`。分两种认证方式：

### 8.1 内部 API（Bearer Token 鉴权）

路由前缀 `/api/`，需 `Authorization: Bearer <token>` 请求头。开发模式下 Token 为空时跳过认证。**仅限本机调用，不对外暴露**。

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/status | 服务状态，返回 version 与 status |
| POST | /api/task | 创建任务，body 含 app_id、type、mode、params |
| GET | /api/task/{id} | 任务详情 |
| POST | /api/task/{id}/cancel | 取消任务 |
| GET | /api/tasks | 任务列表，query 参数 app_id、status、page、page_size |
| GET | /api/logs | 日志列表，query 参数 app_id、level、page、page_size |
| GET | /api/extensions | 拓展列表 |
| GET | /api/extensions/{name} | 拓展详情 |
| PUT | /api/extensions/{name} | 更新拓展 |
| POST | /api/extensions/{name}/test | 测试拓展 |

### 8.2 开放 API（X-API-Key 鉴权）

路由前缀 `/api/open/`，需 `X-API-Key` 请求头。仅在 `open_api_enabled=true` 且密钥非空时生效。

支持的操作子集（与内部 API 功能一致）：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /api/open/status | 服务状态 |
| POST | /api/open/task | 创建任务 |
| GET | /api/open/tasks | 任务列表 |
| GET | /api/open/task/{id} | 任务详情 |
| POST | /api/open/task/{id}/cancel | 取消任务 |

> 开放 API 适用于跨服务器调用场景（如 A 服务器向 B 服务器的本地服务提交任务）。使用前需在基本设置中启用开放 API 并配置密钥。

### 任务创建请求格式

```json
{
    "app_id": "cmspro.douyindown",
    "type": "system.exec",
    "mode": "sync",
    "params": {
        "command": "dir C:\\",
        "timeout": 10
    }
}
```

`mode` 取值：

- `sync`：同步执行，阻塞返回结果
- `async`：异步执行，立即返回 `task_id`
- 留空：默认 `sync`

### 直接调用示例

```bash
curl -X POST http://127.0.0.1:9501/api/task \
  -H "Authorization: Bearer <your_token>" \
  -H "Content-Type: application/json" \
  -d '{
    "app_id": "cmspro.localservice",
    "type": "system.info",
    "mode": "sync",
    "params": {}
  }'
```

> **建议**：PHP 应用统一通过 `LocalServiceClient` 调用，不要直接 curl，以免遗漏 Token 与错误兜底逻辑。

***

## 九、数据存储

### 9.1 PHP 端（MySQL）

仅一张表，由应用迁移管理：

**`app_cmspro_localservice_custom_scripts`** — 指令库

字段定义见第六章 6.1 节。

### 9.2 Go 端（SQLite）

数据库文件 `storage/local_service.db`，由 Go 服务自动创建与初始化。

**tasks 表** — 任务记录

| 字段 | 类型 | 说明 |
|------|------|------|
| id | TEXT | UUID 主键 |
| app_id | TEXT | 调用方应用 ID |
| type | TEXT | 任务类型 |
| mode | TEXT | sync / async |
| params | TEXT | 任务参数 JSON |
| status | TEXT | pending / running / success / failed / cancelled |
| result | TEXT | 任务结果 JSON |
| error | TEXT | 错误信息 |
| create_time | DATETIME | 创建时间 |
| start_time | DATETIME | 开始执行时间 |
| finish_time | DATETIME | 完成时间 |
| duration_ms | INTEGER | 耗时毫秒 |

索引：`idx_tasks_app_id`、`idx_tasks_status`。

**logs 表** — 日志记录

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER | 自增主键 |
| task_id | TEXT | 关联任务 ID，可为空 |
| app_id | TEXT | 应用 ID |
| level | TEXT | info / warn / error / debug |
| message | TEXT | 日志内容 |
| context | TEXT | 上下文 JSON |
| create_time | DATETIME | 创建时间 |

索引：`idx_logs_app_id`。日志按 `log_retention_days` 配置自动清理。

**extensions 表** — 拓展配置

| 字段 | 类型 | 说明 |
|------|------|------|
| id | INTEGER | 自增主键 |
| name | TEXT | 拓展标识，唯一 |
| title | TEXT | 显示名称 |
| type | TEXT | browser / media / script |
| path | TEXT | 可执行文件路径 |
| config | TEXT | JSON 配置 |
| status | INTEGER | 0=禁用，1=启用 |
| create_time / update_time | DATETIME | 时间戳 |

> 拓展表数据由 Go 端 `initDefaultExtensions()` 在内存中初始化，持久化到 SQLite。PHP 端通过 HTTP 查询与修改。

***

## 十、完整调用示例

### 10.1 抖音扫码登录（浏览器自动化）

```php
use App\Apps\CmsproLocalservice\Services\LocalServiceClient;

$client = app(LocalServiceClient::class);
$appId  = 'cmspro.douyindown';

// 1. 打开浏览器（有头模式便于扫码）
$res = $client->exec($appId, 'browser.open', ['headless' => false]);
if ($res['code'] !== 0) {
    throw new \RuntimeException('打开浏览器失败: ' . $res['message']);
}
$browserId = $res['data']['browser_id'];

try {
    // 2. 导航到抖音登录页
    $client->exec($appId, 'browser.navigate', [
        'browser_id'   => $browserId,
        'url'          => 'https://www.douyin.com/user/self',
        'wait_seconds' => 3,
    ]);

    // 3. 等待二维码出现
    $client->exec($appId, 'browser.wait_for', [
        'browser_id' => $browserId,
        'selector'    => '#animate_qrcode_container',
        'timeout'     => 15,
    ]);

    // 4. 截取二维码
    $shot = $client->exec($appId, 'browser.screenshot', [
        'browser_id' => $browserId,
        'selector'    => '#animate_qrcode_container',
    ]);
    $qrcodeBase64 = $shot['data']['image'] ?? '';

    // 5. 轮询登录状态（业务逻辑）
    // ...

} finally {
    // 6. 必须关闭浏览器，释放 Chrome 进程
    $client->exec($appId, 'browser.close', ['browser_id' => $browserId]);
}
```

### 10.2 异步备份任务

```php
// 提交异步任务
$taskId = $client->execAsync('cmspro.backup', 'system.script', [
    'script'  => 'mysqldump -uroot cmspro > D:\\backup.sql',
    'timeout' => 300,
]);

// 后续轮询状态
if ($taskId) {
    $status = $client->getTaskStatus($taskId);
    // $status['data']['status'] = 'success' / 'failed' / 'running'
}
```

### 10.3 文件读取与进程管理

```php
// 读取配置文件
$res = $client->exec('cmspro.flow', 'file.read', [
    'path'     => 'D:\\config\\app.json',
    'encoding' => 'utf8',
]);
$config = json_decode($res['data']['content'], true);

// 启动外部进程
$res = $client->exec('cmspro.flow', 'process.start', [
    'executable' => 'notepad.exe',
    'args'       => ['D:\\notes.txt'],
]);
$pid = $res['data']['pid'];

// 按名称终止进程
$client->exec('cmspro.flow', 'process.kill_by_name', ['name' => 'notepad.exe']);
```

***

## 十一、安全注意事项

### 11.1 监听地址

Go 服务内部 API **仅监听 `127.0.0.1`**，不对外网暴露。生产环境务必确认 Nginx/防火墙未将 9501 端口转发到公网。

开放 API（`/api/open/`）同样监听 `127.0.0.1:9501`，如需从其他服务器调用，需通过反向代理暴露并配合 `X-API-Key` 认证。

### 11.2 Token 与 API Key 鉴权

- 内部 API：安装时自动生成 32 位随机 Token，存于 `config_items`，所有 HTTP 请求携带 `Authorization: Bearer <token>` 头
- 开放 API：需在基本设置中手动启用，自动生成 32 位随机密钥，请求携带 `X-API-Key` 头
- Token/Key 为空时 Go 端跳过认证（仅用于开发调试，**生产环境必须配置**）
- Token 可在「基本配置」页面重新生成，生成后所有调用方自动使用新 Token
- 开放 API 密钥可在「基本配置」页面重新生成

### 11.3 命令执行风险

`system.exec`、`system.script`、`process.start` 等任务具有完整的系统权限（Windows 服务以 LocalSystem 身份运行），调用方必须：

- 对外部输入做严格校验，禁止拼接未授权命令
- 优先使用白名单方式限制可执行的命令范围
- 避免将用户输入直接作为 `command` 或 `script` 参数

### 11.4 文件路径校验

`file.*` 系列任务接受任意绝对路径，调用方需自行限制可访问目录，避免越权读取系统敏感文件。

### 11.5 浏览器实例回收

每个 `browser.open` 都会启动一个 Chrome 进程，**必须**通过 `browser.close` 显式关闭。建议使用 `try/finally` 保证异常路径下也能释放资源。Go 端不会自动回收孤立浏览器实例。

### 11.6 审计日志

所有任务和日志按 `app_id` 隔离并持久化，可在后台追溯。调用方应传入真实的应用 ID，便于问题排查与责任定位。日志按配置天数自动清理，避免磁盘占满。

***

## 十二、扩展开发指南

### 12.1 新增任务处理器（Go 端）

任务处理器实现 `TaskHandler` 接口，通过 `init()` 注册：

```go
package task

import (
    "context"
    "fmt"
)

type MyTaskHandler struct{}

func (h *MyTaskHandler) Execute(ctx context.Context, params map[string]interface{}) (*TaskResult, error) {
    name, _ := params["name"].(string)
    if name == "" {
        return nil, fmt.Errorf("缺少 name 参数")
    }
    return &TaskResult{
        Success: true,
        Data:    map[string]interface{}{"greeting": "hello, " + name},
    }, nil
}

func init() {
    Register("myapp.greet", &MyTaskHandler{})
}
```

处理器需放在 `go-src/internal/task/` 目录下，重新编译二进制后部署。

### 12.2 新增拓展（Go 端）

在 `initDefaultExtensions()` 中注册新拓展：

```go
mgr.Register(&Extension{
    Name:   "nodejs",
    Title:  "Node.js",
    Type:   "script",
    Path:   "",
    Config: "{}",
    Status: 0, // 默认禁用
})
```

如需特殊逻辑（如 Chrome 的配置驱动），实现对应的 Extension 结构并接入 `Manager.Update()` 分支。

### 12.3 重新编译 Go 二进制

在 `go-src` 目录下执行：

```bash
# Windows 环境
set GOOS=windows
set GOARCH=amd64
go build -o ../Bin/windows/cmspro-local-service.exe ./cmd/service
```

编译产物覆盖 `Bin/windows/cmspro-local-service.exe`，重启服务生效。

### 12.4 从其他应用调用

任意 CmsPro 应用只需依赖 `cmspro.localservice`，通过服务容器注入客户端：

```php
// 在应用的 ServiceProvider::register() 中无需重复注册
// LocalServiceClient 已由 CmsproLocalservice 注册为全局单例

use App\Apps\CmsproLocalservice\Services\LocalServiceClient;

class MyService
{
    public function __construct(
        private LocalServiceClient $localService
    ) {}

    public function doSomething(): void
    {
        $res = $this->localService->exec('cmspro.myapp', 'system.info');
        // ...
    }
}
```

### 12.5 调用方注意事项

- `app_id` 必须传入调用方自身应用 ID，便于审计与过滤
- 同步任务默认超时 30 秒，可在 `params.timeout` 中覆盖
- 网络异常统一返回 `code=503`，调用方应做降级处理
- 长耗时任务用 `execAsync()` 提交，避免阻塞 PHP 请求
- 浏览器自动化场景必须显式 `browser.close`，防止资源泄漏

***

## 十三、错误码参考

| code | 含义 | 处理建议 |
|------|------|---------|
| 0 | 成功 | — |
| 400 | 请求参数错误 | 检查参数完整性与类型 |
| 401 | 认证失败 | 检查 Token/API Key 配置是否一致 |
| 403 | 开放 API 未启用 | 在基本设置中启用开放 API 并配置密钥 |
| 404 | 资源不存在 | 确认任务 ID 或拓展名称 |
| 405 | 方法不允许 | 检查 HTTP 方法 |
| 422 | 参数校验失败 | 检查请求体中的业务参数（如脚本平台不匹配） |
| 500 | 服务内部错误 | 查看 Go 服务日志排查 |
| 503 | 服务不可用 | Go 服务未启动或网络异常，检查服务状态 |

PHP 端错误码与 Go 端一致，网络异常由 `LocalServiceClient` 统一转为 503。

***

## 十四、常见问题

### Q1：服务启动后 HTTP 探测不通？

- 确认 Windows 服务状态为 `running`（`sc query CmsProLocalService`）或 Linux systemd 状态为 `active`
- 确认端口未被占用（`netstat -ano | findstr :9501` 或 `ss -tlnp | grep 9501`）
- 确认 `storage/local_service_config.json`（以及 `Bin/storage/local_service_config.json`）中端口与 PHP 端配置一致
- 查看 Windows 事件查看器或 Linux journalctl 中的服务日志

### Q2：开放 API 调用返回 403？

- 在「基本配置」页面检查开放 API 是否已启用
- 确认 `X-API-Key` 请求头已携带且与配置页显示的密钥一致
- 修改密钥后需重启 Go 服务生效
- 确认请求路径前缀为 `/api/open/`（非 `/api/`）

### Q3：浏览器任务报「Chrome 拓展未启用」？

在「拓展管理」页面将 chrome 拓展状态改为启用，或通过 API 启用：

```php
$client->updateExtension('chrome', ['status' => 1]);
```
### Q4：Windows 命令输出乱码？

`system.exec` 已做 GBK→UTF-8 转码。若仍乱码，检查脚本是否通过 `system.script` 执行（该任务类型未做转码，需在 PHP 端用 `ScriptService::execute()` 调用，或自行通过 `mb_convert_encoding` 处理）。

### Q5：异步任务提交后状态一直是 pending？

- 检查 Go 服务日志（`storage/logs/` 或事件查看器）是否有执行异常
- 确认任务类型已注册（未注册会返回「不支持的任务类型」错误）
- 确认处理器 `Execute()` 没有死锁或永久阻塞

### Q6：PHP 端调用一直返回 503？

- 确认 Go 服务在运行（`isOnline()` 返回 false 即服务未启动）
- 确认 Token 一致：PHP 端 `config_items` 与 Go 端 `local_service_config.json` 中的 `auth_token` 必须相同
- 修改 Token 或端口后，需在「基本配置」页面保存触发 `syncConfig()`，并重启 Go 服务

### Q7：如何清理历史任务和日志？

- 日志：Go 服务每 24 小时自动清理超过 `log_retention_days` 的记录
- 任务：当前未提供自动清理，可在 SQLite 中手动 `DELETE FROM tasks WHERE create_time < ...`
- 临时脚本：`storage/app/localservice/` 下的 bat 脚本可手动删除

***

## 附录：目录结构

```
app/Apps/CmsproLocalservice/
├── manifest.json                          应用清单
├── Install.php                            安装/卸载/升级脚本
├── ServiceProvider.php                    服务提供者（路由、视图、单例注册）
├── icon.svg                               应用图标
├── Config/
│   └── localservice.php                   默认配置
├── Bin/
│   ├── storage/
│   │   ├── local_service.db               SQLite 预置数据库
│   │   └── local_service_config.json      预置配置（syncConfig 同步目标）
│   ├── windows/
│   │   └── cmspro-local-service.exe       Windows x86_64 编译产物
│   └── linux/
│       ├── cmspro-local-service           Linux x86_64 编译产物
│       └── cmspro-local-service-arm64     Linux ARM64 编译产物
├── Controllers/
│   └── Admin/
│       ├── DashboardController.php        仪表盘页面
│       ├── TaskController.php             任务记录页面
│       ├── LogController.php              日志查看页面
│       ├── ExtensionController.php        拓展管理页面
│       ├── ScriptController.php           指令管理页面
│       ├── SettingController.php          基本配置页面（含开放 API）
│       └── Api/
│           ├── ServiceApiController.php   服务控制 API
│           ├── SettingApiController.php   设置 API（含开放 API 密钥生成）
│           ├── TaskApiController.php      任务 API
│           ├── LogApiController.php       日志 API
│           ├── ExtensionApiController.php 拓展 API
│           └── ScriptApiController.php    指令库 API（含 platform 筛选）
├── Models/
│   └── CustomScript.php                   指令库模型（含 platform 字段）
├── Migrations/
│   ├── 2026_07_02_000001_create_custom_scripts_table.php
│   ├── 2026_07_03_000001_add_platform_to_custom_scripts_table.php
│   └── 2026_07_03_000002_add_open_api_config_items.php
├── Routes/
│   ├── admin.php                          后台页面路由
│   └── admin_api.php                      后台 API 路由
├── Services/
│   ├── LocalServiceClient.php             统一 HTTP 客户端（核心）
│   ├── ServiceManager.php                 服务生命周期管理（Win+Linux 双平台）
│   └── ScriptService.php                  指令库管理（含 platform 校验）
├── Views/Admin/
│   ├── dashboard/index.blade.php
│   ├── tasks/index.blade.php
│   ├── logs/index.blade.php
│   ├── extensions/index.blade.php
│   ├── scripts/index.blade.php
│   └── settings/index.blade.php
└── go-src/                                Go 服务源码
    ├── go.mod / go.sum
    ├── cmd/service/
    │   ├── main_windows.go                Windows 入口（SCM 检测 + 命令行）
    │   └── main_linux.go                  Linux 入口
    └── internal/
        ├── api/                           HTTP 路由与处理器
        │   ├── server.go                  路由注册（内部 API + 开放 API）
        │   ├── handlers.go                请求处理器
        │   └── middleware.go              认证（Bearer Token + X-API-Key）
        ├── config/config.go               配置加载与持久化（含 OpenAPI 字段）
        ├── extension/                     拓展管理
        │   ├── manager.go
        │   └── chrome.go                  Chrome 拓展
        ├── storage/                       SQLite 持久化
        │   ├── sqlite.go
        │   ├── tasks.go
        │   ├── logs.go
        │   └── extensions.go
        ├── task/                          任务处理器
        │   ├── types.go                   TaskHandler 接口与注册表
        │   ├── manager.go                 同步/异步执行
        │   ├── file.go                    file.*
        │   ├── system.go                  system.*
        │   ├── process.go                 process.*
        │   ├── service.go                 service.*  (仅 Windows)
        │   └── browser.go                 browser.*
        └── winservice/service.go          Windows 服务封装
```

***

## 十五、版本历史

| 版本 | 日期 | 说明 |
|------|------|------|
| 1.2.0 | 2026-08-12 | 文档审查更新：补充后台 API 中新增的接口（服务重启、已安装应用列表、任务/日志清空、拓展命令安装与默认安装命令查询、指令库初始化）；完善版本历史章节。应用版本 1.0.1 |
| 1.1.0 | 2026-07-03 | 新增开放 API 能力（X-API-Key 鉴权、`/api/open/` 路由）、指令库 platform 字段、Linux 平台支持说明 |
| 1.0.0 | 2026-07-02 | 初始版本，Windows 本地服务基础能力 |
