# CmsPro-本地服务-MCP服务指南

> 版本：1.0.0 | 更新日期：2026-10-22
> 应用版本：1.3.0 | 应用 ID：cmspro.localservice

---

## 一、概述

本地服务把**本机能力**（系统信息、文件操作、进程与服务管理、受控浏览器）通过 **MCP（Model Context Protocol）** 协议开放给外部 AI 工具（Claude Code、Trae、Cursor、Codex 等），让 AI 能直接操作运行本地服务的这台机器。

设计定位与宝塔 MCP 一致：**把面板能力交给 AI 调用，但开放哪些能力由你决定**。

| 特性 | 说明 |
|------|------|
| 协议 | MCP 2025-03-26，Streamable HTTP 传输，JSON-RPC 2.0 |
| 端点 | `POST {前缀}/mcp`，前缀为随机生成的 `/mcp-{12位}`（防端口扫描） |
| 鉴权 | Bearer Token（访问凭据），逐凭据独立授权工具集 |
| 访问控制 | IP 白名单 + 滑动窗口限流 + 启用开关 |
| 安全确认 | 高风险工具首次调用返回 `waiting_confirm`，需用户明确确认后执行 |
| 审计 | MCP 调用写入本地服务日志（含来源 IP、凭据标识、工具、结果、耗时） |
| 部署 | Go 服务内同进程实现（`internal/mcp/`），仅监听 `127.0.0.1` |

> **与 AI 透传（llmrelay）的区别**：llmrelay 是让 CmsPro 承接 LLM 流式转发（数据面/控制面分离）；MCP 服务是让本机能力被外部 AI 调用。二者互相独立、可同时使用。

---

## 二、快速接入

```
步骤 1：后台「本地服务 → MCP 服务」
步骤 2：切到「安全授权」Tab → 打开「启用服务」开关
步骤 3：按需调整 IP 白名单（默认 * 放行全部，建议改为 AI 工具所在机器的出口 IP）
步骤 4：在「访问凭据」中「创建凭据」，复制令牌（仅显示一次）
步骤 5：回到「接入与体验」Tab，复制「接入提示词」发给 AI 工具
步骤 6：AI 工具自动完成配置；先用只读工具（如 SystemInfo）验证连通性
```

**手动配置参考**（各 Agent 配置文件位置不同，以下为通用结构）：

```json
{
  "mcpServers": {
    "cmspro-local": {
      "url": "http://<服务器地址>:9501/mcp-xxxxxxxxxxxx/mcp",
      "headers": {
        "Authorization": "Bearer cmspro_xxxxxxxx"
      },
      "disabled": false
    }
  }
}
```

> 路由前缀可在「接入与体验」Tab 点「重新生成」更换；更换后所有客户端需同步更新地址。

---

## 三、工具清单与风险分级

当前共 **32 个工具**，按风险分三级：

| 风险 | 数量 | 含义 | 默认授权 |
|------|------|------|----------|
| 低风险 | 12 | 只读查询，无副作用 | ✅ 自动授权 |
| 中风险 | 11 | 可恢复的修改（服务启停、浏览器交互） | ❌ 需手动勾选 |
| 高风险 | 9 | 破坏性操作（写删文件、启停进程、执行命令） | ❌ 需手动勾选 + 调用需确认 |

分类分布：系统 4、文件 9、进程 4、服务 4、浏览器 11。

### 3.1 系统（4）

| 工具 | 名称 | 风险 | 需确认 |
|------|------|------|--------|
| `SystemInfo` | 获取系统信息 | 低 | - |
| `SystemNetwork` | 获取网络信息 | 低 | - |
| `SystemExec` | 执行系统命令 | **高** | ✓ |
| `SystemScript` | 执行脚本 | **高** | ✓ |

### 3.2 文件（9）

| 工具 | 名称 | 风险 | 需确认 |
|------|------|------|--------|
| `FileRead` | 读取文件 | 低 | - |
| `FileList` | 列出目录 | 低 | - |
| `FileExists` | 判断文件是否存在 | 低 | - |
| `FileInfo` | 获取文件信息 | 低 | - |
| `FileCopy` | 复制文件 | 中 | - |
| `FileMkdir` | 创建目录 | 中 | - |
| `FileWrite` | 写入文件 | **高** | ✓ |
| `FileDelete` | 删除文件 | **高** | ✓ |
| `FileMove` | 移动/重命名文件 | **高** | ✓ |

### 3.3 进程（4）

| 工具 | 名称 | 风险 | 需确认 |
|------|------|------|--------|
| `ProcessList` | 列出进程 | 低 | - |
| `ProcessStart` | 启动进程 | **高** | ✓ |
| `ProcessKill` | 终止进程 | **高** | ✓ |
| `ProcessKillByName` | 按名称终止进程 | **高** | ✓ |

### 3.4 服务（4）

| 工具 | 名称 | 风险 | 需确认 |
|------|------|------|--------|
| `ServiceList` | 列出系统服务 | 低 | - |
| `ServiceStatus` | 查询服务状态 | 低 | - |
| `ServiceStart` | 启动服务 | 中 | - |
| `ServiceStop` | 停止服务 | 中 | - |

### 3.5 浏览器（11）

| 工具 | 名称 | 风险 | 需确认 |
|------|------|------|--------|
| `BrowserScreenshot` | 页面截图 | 低 | - |
| `BrowserGetCookies` | 获取 Cookie | 低 | - |
| `BrowserWaitFor` | 等待页面元素 | 低 | - |
| `BrowserOpen` | 打开浏览器 | 中 | - |
| `BrowserClose` | 关闭浏览器 | 中 | - |
| `BrowserNavigate` | 浏览器跳转 | 中 | - |
| `BrowserSetCookies` | 设置 Cookie | 中 | - |
| `BrowserSetHeaders` | 设置请求头 | 中 | - |
| `BrowserClick` | 点击页面元素 | 中 | - |
| `BrowserInput` | 输入文本 | 中 | - |
| `BrowserExecute` | 执行页面脚本 | **高** | ✓ |

---

## 四、安全授权

### 4.1 外部访问

| 配置 | 说明 |
|------|------|
| **启用服务** | 总开关；停用后所有 MCP 客户端无法连接 |
| **IP 白名单** | 支持 `*`（放行全部）、单 IP、CIDR（`10.0.0.0/8`）、IP 区间（`192.168.1.1-192.168.1.50`）。**留空拦截所有**（安全默认） |
| **调用限流** | 按「凭据 + 来源 IP」滑动窗口计数，默认 300 次 / 60 秒，超出返回 429 |
| **路由前缀** | 随机 `/mcp-{12位}`，降低被扫描风险，可重新生成 |

> IP 白名单仅当请求对端为回环地址时才信任 `X-Real-IP` 头（防伪造）；其余场景一律用直连对端 IP。

### 4.2 访问凭据

每个凭据独立维护权限，互不影响。

| 字段 | 说明 |
|------|------|
| 名称 | 展示用标识（如「我的 Trae」） |
| KEY | 掩码展示（仅尾 4 位），创建时返回一次明文 |
| 有效期 | 永不过期 / 7 天 / 30 天 / 90 天 |
| 已授权 | 生效工具数 / 工具总数 |
| 状态 | 运行中 / 已停用 / 已过期 |

**权限管理弹窗**：按分类折叠展示全部工具，可勾选，支持「仅授权低风险」「全选」「清空」。

**默认授权策略**：新建凭据仅授权**低风险工具**；中/高风险需显式勾选（opt-in）。这一设计避免 AI 在未授权的情况下执行破坏性操作。

### 4.3 二次确认（高风险工具）

高风险工具（标记「需确认」）首次调用时：

1. Go 服务返回 `state=waiting_confirm`，**不执行**，并附 `instruction` 指导 AI；
2. AI 必须停止调用，向用户说明将要执行的操作与影响，等待用户**后续消息明确同意**；
3. 用户同意后，AI 用**完全相同的业务参数**并设置 `confirm=true` 再次调用，才真正执行。

**安全细节**：

- 确认键 = 凭据 + 工具 + 完整业务参数（哈希），**参数任一变化都需重新确认**（防止「先确认 A 却执行 B」）；
- 确认记录 **TTL 600 秒**，过期需重新发起；
- 确认记录**一次性消费**，防并发/重试双执行；
- 确认记录**按凭据隔离**，凭据 A 的确认不能用于凭据 B。

---

## 五、实现架构

```
外部 AI 工具（Claude Code / Trae / Cursor …）
    │ POST {prefix}/mcp   Bearer Token
    ▼
Go 服务（CmsproLocalservice，127.0.0.1:9501）
    ├─ internal/mcp/server.go       协议层：JSON-RPC 分发（initialize / tools/list / tools/call）
    ├─ internal/mcp/registry.go     注册表：工具元数据、风险分级、结果体积限制
    ├─ internal/mcp/tools.go        工具集：映射既有任务处理器（system.* / file.* / process.* …）
    ├─ internal/mcp/credentials.go  凭据与权限矩阵（tool_ids ∩ 注册集 - disabled）
    ├─ internal/mcp/gate.go         门控：确认闸、IP 白名单、滑动窗口限流
    └─ 复用 internal/task/          执行链路与 /api/task 完全一致（含任务落库与日志）
    ▼
本机能力（文件系统 / 进程 / 服务 / 受控浏览器）

PHP 后台（配置真源）
    ├─ Models/McpKey.php            凭据模型与权限矩阵规范化
    ├─ Services/McpService.php      工具清单、配置读写、凭据 CRUD、配置下发
    ├─ Controllers/Admin/Api/McpApiController.php   管理端点
    └─ Views/Admin/mcp/index.blade.php              管理界面
```

**配置流向**：PHP 后台写入 `storage/local_service_config.json` 的 `mcp` 段 → Go 服务启动时读取 → 生效。因此**凭据或访问控制变更后需重启本地服务**（后台会提示）。

**工具清单获取**：后台优先调 Go 服务 `/api/mcp/tools` 实时获取（保证与 Go 注册表一致）；服务离线时回落内置快照，使后台在服务未启动时仍可查看与配置权限。

---

## 六、后台管理端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/admin/cmspro/localservice/mcp/tools` | 工具清单（含分类分组与风险统计） |
| GET | `/api/admin/cmspro/localservice/mcp/config` | 访问控制配置（含端点地址与提示词） |
| POST | `/api/admin/cmspro/localservice/mcp/config` | 保存访问控制配置 |
| POST | `/api/admin/cmspro/localservice/mcp/config/regenerate-prefix` | 重新生成路由前缀 |
| GET | `/api/admin/cmspro/localservice/mcp/prompt` | 获取接入提示词 |
| POST | `/api/admin/cmspro/localservice/mcp/sync` | 手动下发配置 |
| GET | `/api/admin/cmspro/localservice/mcp/keys` | 凭据列表（令牌掩码） |
| GET | `/api/admin/cmspro/localservice/mcp/keys/{id}?with_token=1` | 凭据详情（可选返回明文令牌） |
| POST | `/api/admin/cmspro/localservice/mcp/keys` | 创建凭据（返回一次明文令牌） |
| PUT | `/api/admin/cmspro/localservice/mcp/keys/{id}` | 更新凭据（名称/有效期/启停/授权工具） |
| DELETE | `/api/admin/cmspro/localservice/mcp/keys/{id}` | 删除凭据 |
| POST | `/api/admin/cmspro/localservice/mcp/keys/{id}/toggle` | 切换启停 |
| POST | `/api/admin/cmspro/localservice/mcp/keys/{id}/low-risk-only` | 仅授权低风险工具 |
| POST | `/api/admin/cmspro/localservice/mcp/keys/{id}/restore-default` | 恢复默认授权 |

**Go 侧内部端点**（Bearer 认证，供后台调用）：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/mcp/tools` | 工具清单与风险统计 |
| GET | `/api/mcp/config` | 运行配置（凭据仅返回数量） |

---

## 七、验证

```bash
# 1. 握手（应返回 protocolVersion / capabilities / serverInfo / instructions）
curl -X POST http://127.0.0.1:9501/mcp-xxxxxxxxxxxx/mcp \
  -H "Authorization: Bearer cmspro_xxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'

# 2. 工具发现（应按凭据授权集过滤）
curl -X POST http://127.0.0.1:9501/mcp-xxxxxxxxxxxx/mcp \
  -H "Authorization: Bearer cmspro_xxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'

# 3. 只读工具调用（读取系统信息）
curl -X POST http://127.0.0.1:9501/mcp-xxxxxxxxxxxx/mcp \
  -H "Authorization: Bearer cmspro_xxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"SystemInfo","arguments":{}}}'

# 4. 高风险工具首次调用（应返回 waiting_confirm 且不执行）
curl -X POST http://127.0.0.1:9501/mcp-xxxxxxxxxxxx/mcp \
  -H "Authorization: Bearer cmspro_xxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"FileDelete","arguments":{"path":"/tmp/test.txt"}}}'
```

预期：步骤 4 返回 `state=waiting_confirm`、`executed=false` 与 `instruction`；此时目标文件**未被删除**。用户确认后再以 `confirm=true` 调用才会实际执行。

---

## 八、常见问题

| 现象 | 原因与处理 |
|------|-----------|
| 401 unauthorized | 凭据无效/已停用/已过期；检查 Token 与凭据状态 |
| 403 ip denied | 来源 IP 不在白名单；在「安全授权 → IP 白名单」添加或改为 `*` |
| 429 rate limited | 触发限流；调高「调用限流」阈值或等待窗口结束 |
| 503 MCP 服务未启用 | 在「安全授权」打开「启用服务」开关并重启本地服务 |
| 工具返回「该凭据未授权调用工具」 | 该工具未授权给此凭据；在凭据「权限」中勾选（高风险需谨慎） |
| 工具返回「基础工具不提供给当前客户端」 | 保留的基础工具（basic_tool）在当前部署模式下不对外提供 |
| 配置改了不生效 | 配置经配置文件下发，**需重启本地服务**；后台保存时会提示 |
| 工具结果超过 50KB 限制 | 缩小查询范围（如分页读取、按名称过滤进程） |
| 客户端连不上 | 确认本地服务运行中、端口正确、路由前缀与后台一致（前缀可重新生成过） |

---

## 九、版本历史

| 版本 | 日期 | 说明 |
|------|------|------|
| 1.0.0 | 2026-10-22 | 首版（应用 1.3.0）。自研 MCP 协议服务（Streamable HTTP）：32 个工具按低/中/高三级风险分级（12/11/9），低风险默认授权、中高风险 opt-in，高风险调用需二次确认（确认键含完整参数哈希、TTL 600s、一次性消费、按凭据隔离）；「凭据 × 工具」权限矩阵、IP 白名单（支持 `*`/CIDR/区间）、滑动窗口限流、随机路由前缀、调用审计落库。Go 侧同进程实现并复用既有任务处理器（与 `/api/task` 同链路）；PHP 后台新增「MCP 服务」菜单（接入与体验 / 安全授权 两 Tab）与凭据权限管理弹窗。Go 55 用例 + PHP 33 用例，共 88 用例覆盖 |