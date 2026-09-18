# CmsPro-本地服务-AI透传扩展指南

> 版本：1.0.0 | 更新日期：2026-09-18
> 适用应用版本：cmspro.localservice >= 1.1.0

## 一、概述

AI 透传扩展（LLM Relay）是 CmsproLocalservice Go 服务内置的**通用 LLM 流式透传处理器**，用于解决 PHP 侧直接转发 LLM SSE 流时的问题：

| 问题 | 说明 |
|------|------|
| php-cgi worker 占用 | Windows 下 php-cgi 进程池有限，每个长 SSE 流独占一个 worker，多个 AI 对话并行时进程池被填满，全站响应极慢 |
| 进程被强杀 | PHP-FPM `request_terminate_timeout`、Nginx `fastcgi_read_timeout` 等超时链路多，长任务易被中途杀掉 |
| 重试逻辑与业务耦合 | 切号重试、出口调度、日志落库全部挤在 PHP 流式响应生命周期内 |

**核心思路：数据面与控制面分离。**

```
客户端（Trae/Cline/Cursor 等）
    │ SSE 流
    ▼
Go 服务（CmsproLocalservice，127.0.0.1:9501）
    │  ① 回调 PHP 决策接口（选号/出口/凭证）      ──► CmsPro PHP（业务面）
    │  ② goroutine 直连上游 LLM，双向转发 SSE 流   ──► 上游（api.cline.bot 等）
    │  ③ 流结束后回调 PHP 日志接口（落库/账号维护） ──► CmsPro PHP（业务面）
    ▼
客户端收到完整 OpenAI 兼容响应
```

- **Go（数据面）**：承接 SSE 双向转发、首字节/空闲超时、心跳、换号重试，goroutine 轻量不占 PHP 进程池；
- **PHP（控制面）**：保留全部业务——鉴权、选号、出口调度、Token 刷新、日志落库、账号维护、后台管理，业务代码零改动即可复用。

## 二、Go 侧端点（通用路由，所有应用共用）

Go 服务按 `/open/llm/{app}/{proxy_prefix}/*path` 通用路由接入任意应用，无需为每个应用改 Go 代码：

| 端点 | 方法 | 说明 |
|------|------|------|
| `/open/llm/ping` | GET | 健康检查，返回 `{"code":0,"data":{"service":"llm-relay","status":"ok"}}` |
| `/open/llm/{app}/{prefix}/*path` | POST/GET | 透传端点。`{app}` 为应用标识（如 `cline`），`{prefix}` 为版本前缀（如 `v1`），`*path` 原样透传（如 `chat/completions`） |

以 Clineproxy 为例，Go 透传端点为：

```
http://127.0.0.1:9501/open/llm/cline/v1/chat/completions
http://127.0.0.1:9501/open/llm/cline/v1/models
```

> 客户端原本指向 `http://www.cmspro.com/api/cline/v1/chat/completions` 的地址，手动替换为上述 Go 端点即可（鉴权密钥不变）。

**实现位置**：`go-src/internal/llmrelay/relay.go`，路由注册于 `go-src/internal/api/server.go`（`/open/llm/` 前缀分支）。

## 三、应用接入契约（PHP 侧需实现两接口）

接入应用需提供两个内部接口，Go 每处理一个请求都会依次调用：

### 3.1 决策接口：`POST /api/{应用路由前缀}/internal/dispatch`

Go 在收到客户端请求后、连接上游前调用。请求头携带：

| 请求头 | 说明 |
|--------|------|
| `Authorization` | 客户端原样透传（由应用自行校验 proxy_api_key） |
| `X-Cmspro-Dispatch-Path` | 客户端请求的 path（如 `chat/completions`），供应用拼接上游地址 |
| `Content-Type` | `application/json` |

请求体：

```json
{
  "model": "deepseek/deepseek-v4-flash",
  "stream": true,
  "tried_account_ids": [1, 2]
}
```

| 字段 | 说明 |
|------|------|
| `model` | 客户端请求的模型（可选） |
| `stream` | 是否流式 |
| `tried_account_ids` | 已试过的账号 ID 列表。Go 换号重试时回传，应用应排除这些账号重新决策 |

成功响应（HTTP 200，`code=0`）：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "dispatch_id": "b3f1c2d4e5f6...64位随机串",
    "upstream_url": "https://api.cline.bot/api/v1/chat/completions",
    "auth_header": "Bearer eyJhbGciOi...",
    "auth_extra": {"client_id": "...", "X-API-Version": "..."},
    "proxy": {"type": "http", "host": "1.2.3.4", "port": 8080, "username": "", "password": ""},
    "account_id": 7,
    "proxy_ip": "1.2.3.4:8080",
    "retry_policy": {
      "max_retries": 3,
      "first_byte_timeout_ms": 30000,
      "read_timeout_ms": 60000,
      "heartbeat_interval_ms": 10000,
      "first_byte_heartbeat_ms": 3000,
      "long_task_threshold_ms": 60000
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `dispatch_id` | 本次转发的唯一租约 ID（应用侧生成，Go 回调时原样带回） |
| `upstream_url` | 完整上游地址（含版本前缀；应用负责剥离本地 proxy_prefix 后拼接 api_base） |
| `auth_header` | 上游 `Authorization` 头完整值 |
| `auth_extra` | 附加请求头键值对（可为空对象），Go 会逐个附加到上游请求 |
| `proxy` | 出口代理（`type` 填协议 http/socks5；无代理传 `null` 即直连） |
| `account_id` | 本次决策使用的账号 ID（日志回调原样带回） |
| `proxy_ip` | 出口 IP 展示值（仅日志用） |
| `retry_policy` | 重试策略（毫秒），字段含义见下表 |

**retry_policy 字段**：

| 字段 | 说明 |
|------|------|
| `max_retries` | 换号重试上限（Go 重新回调决策接口时回传 tried_account_ids） |
| `first_byte_timeout_ms` | 首字节超时，超时判定连接失败可重试（0=禁用） |
| `read_timeout_ms` | 流空闲超时（无数据判定半开） |
| `heartbeat_interval_ms` | 活跃心跳间隔（向客户端发注释行保活，0=禁用） |
| `first_byte_heartbeat_ms` | 首字节前心跳间隔（0=禁用） |
| `long_task_threshold_ms` | 长流预警阈值（发 `event: notice`，0=禁用） |

失败响应（HTTP 状态码对齐业务码，Go 侧两处校验均要求一致）：

| 业务场景 | 业务码 | HTTP 状态码 |
|----------|--------|-------------|
| 鉴权失败 | 401 | 401 |
| 依赖应用不可用 | 400 | 400 |
| Token 刷新失败 | 502 | 502 |
| 无可用账号/出口 | 429 / 503 | 429 / 503 |

失败后 Go 直接向客户端返回 OpenAI 兼容 error chunk（带 `choices` 字段兼容严格客户端），**不连上游、不发日志回调**。

### 3.2 日志回调接口：`POST /api/{应用路由前缀}/internal/log-callback`

流结束后 Go 异步调用（不影响响应时序）。请求体：

```json
{
  "dispatch_id": "b3f1c2d4e5f6...",
  "status": "success",
  "error_kind": "",
  "error_message": "",
  "http_status": 200,
  "account_id": 7,
  "proxy_ip": "1.2.3.4:8080",
  "model": "deepseek/deepseek-v4-flash",
  "stream": true,
  "client_ip": "127.0.0.1",
  "duration_ms": 5320,
  "first_byte_ms": 812,
  "prompt_tokens": 1024,
  "completion_tokens": 256,
  "total_tokens": 1280,
  "finish_reason": "stop",
  "retries": 0
}
```

| 字段 | 说明 |
|------|------|
| `status` | `success` 流完整结束 / `interrupted` 客户端中断 / `failed` 上游失败（Go 已按 retry_policy 重试穷尽后） |
| `error_kind` | 错误归类：`auth`(401/403)、`rate_limit`(429)、`upstream`(5xx/读取失败)、`connect`(连接失败)、`timeout`(空闲超时)、`client`(客户端中断)、空=成功 |
| `usage` 三字段 | 来自上游 `stream_options.include_usage` 报告值；上游未返回时为 0 |

响应 HTTP 200 即视为回调成功（响应体不解析）。应用侧应基于 `status`/`error_kind` 做账号与出口维护（参考 3.3）。

### 3.3 租约与安全模型

| 机制 | 说明 |
|------|------|
| 租约写入 | 决策成功后应用侧 `Cache::put('{app}:dispatch:{dispatch_id}', [...], 900)` |
| 租约消费 | 日志回调 `Cache::pull` 原子取出：取不到即伪造/过期/重复回调，返回 404（天然防重放与重复落日志） |
| TTL 防泄漏 | Go 崩溃未回调时，租约 900 秒自动过期；涉及出口槽位占用的应用可借过期机制兜底释放 |
| dispatch_id 强度 | 64 位十六进制随机串（128bit），不可枚举，回调接口无需额外鉴权 |

## 四、应用侧参考实现（以 CmsproClineproxy 为准）

| 组件 | 文件 | 职责 |
|------|------|------|
| 决策服务 | `app/Apps/CmsproClineproxy/Services/DispatchService.php` | 继承业务 ProxyService 复用选号/出口维护方法；鉴权→选号→取凭证→写租约→组装决策载荷 |
| 内部控制器 | `app/Apps/CmsproClineproxy/Controllers/Proxy/InternalController.php` | `dispatch` / `logCallback` 两个动作，HTTP 状态码对齐业务码 |
| 路由 | `app/Apps/CmsproClineproxy/Routes/proxy.php` | `internal/dispatch`、`internal/log-callback` 两条 POST 路由 |
| 设置开关 | `app/Apps/CmsproClineproxy/Controllers/Admin/SettingController.php` | `forward_channel` 白名单项；保存 `go` 前校验 localservice 安装状态，未安装返回 400 引导安装；设置页展示 Go 接入地址与一键复制 |

**其它应用接入清单（通用三步）**：

1. **实现两接口**：参照 `DispatchService` + `InternalController` + 路由，决策接口生成 `dispatch_id` 租约并返回决策载荷，回调接口 pull 租约落日志并按 `error_kind` 维护账号/出口。
2. **设置开关**：在应用 Config 与 `SettingController::ALLOWED_KEYS` 增加 `forward_channel`（默认 `php`）；保存值为 `go` 时校验 `AppModel::where('app_id', 'cmspro.localservice')->installed()->exists()`，未安装返回 400 引导安装；设置页加开关与 Go 接入地址展示（地址拼装：`app(\App\Apps\CmsproLocalservice\Services\ServiceManager::class)->relayBaseUrl() . '/open/llm/{app标识}/{proxy_prefix}/chat/completions'`）。
3. **切换接入**：将客户端代理地址手动替换为 Go 端点（设置页一键复制）。开关值落库后经 `ServiceProvider::overrideConfigFromDatabase()` 自动覆盖运行时 config，PHP 通道行为完全不变。

> **无需修改 Go 代码**：Go 按 `{app}` 段路由，新应用只需在 PHP 侧实现契约即可接入；`cmspro_base_url`（Go 回调 CmsPro 的基础地址）由 `ServiceManager::syncConfig()` 同步到 Go 配置文件，默认取 `config('app.url')`。

## 五、Go 侧行为细则

| 行为 | 说明 |
|------|------|
| 重试边界 | 未向客户端写出任何字节前，上游失败可按 `retry_policy.max_retries` 重新回调决策接口换号；一旦开始写客户端（首字节已出）则不再重试，优雅收尾并回调 `failed` |
| 心跳保活 | 首字节前按 `first_byte_heartbeat_ms`、活跃期按 `heartbeat_interval_ms` 向客户端发送 SSE 注释行（`: keep-alive`），防止客户端/网关空闲断连 |
| usage 提取 | 自定义跨块行扫描器解析 SSE 数据行中的 `usage` 字段，兼容上游分块切割 JSON 行的情况 |
| 非 2xx 响应体 | 上游错误响应体（JSON）作为 `error_message` 回传给 PHP 日志接口，供账号维护判定 |
| 客户端中断 | 客户端断开时停止转发、通知上游关闭，回调 `status=interrupted`、`error_kind=client` |
| 决策重试 | 决策接口 5xx/网络失败时 Go 短暂退避后重试（最多 3 次）；4xx 业务失败直接向客户端返回错误 |

## 六、验证

```bash
# 1. Go 服务健康检查（服务在线且透传处理器已挂载）
curl http://127.0.0.1:9501/open/llm/ping

# 2. 端到端流式验证（替换为实际密钥与模型）
curl -N -X POST http://127.0.0.1:9501/open/llm/cline/v1/chat/completions ^
  -H "Authorization: Bearer <proxy_api_key>" ^
  -H "Content-Type: application/json" ^
  -d "{\"model\":\"deepseek/deepseek-v4-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}],\"stream\":true}"
```

预期：客户端收到完整 SSE 流；应用调用日志出现新记录（经 Go 回调落库）；`php artisan test app/Apps/{AppName}/Tests` 全部通过。

## 七、版本历史

| 版本 | 日期 | 说明 |
|------|------|------|
| 1.0.0 | 2026-09-18 | 首版。Go 侧通用透传处理器（`/open/llm/{app}/*`）+ 两接口契约 + CmsproClineproxy 参考实现 |
