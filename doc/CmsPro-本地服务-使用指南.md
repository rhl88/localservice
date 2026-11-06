# CmsPro-本地服务-使用指南

> 版本：1.2.8 | 更新日期：2026-10-14
> 应用版本：1.2.8 | 应用 ID：cmspro.localservice

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

> **版本来源**：生成安装脚本时自动从版本服务器（`https://v5.cmspro.cn`，由 Versionsvr 应用「本地服务版本管理」提供）获取最新**已发布**版本——本地无二进制或版本落后时自动下载（带大小 + SHA256 校验），已是最新则跳过；版本服务器不可达时降级使用本地已有二进制，不阻塞脚本生成。下载地址与校验值由版本服务器 latest 接口下发，不再使用旧的硬编码静态地址。

> **宝塔加固兼容**：若服务器启用了宝塔「系统加固」等策略，对 `/etc/systemd/system` 设置了 immutable（`chattr +i`），则即便是 root 写入 unit 文件也会报 `Operation not permitted`。安装/卸载脚本会自动检测该加固并临时解除，操作完成后恢复原状，无需手工干预；`chattr`/`lsattr` 不可用时静默跳过。

### 3.2 后台功能验证

后台菜单入口「本地服务」，含以下子模块：

| 菜单 | 路径 | 验证要点 |
|------|------|----------|
| 仪表盘 | `/admin/cmspro/localservice/dashboard` | 服务状态（真实版本号）、任务统计、有新版本时显示「更新」按钮 |
| 任务记录 | `/admin/cmspro/localservice/tasks` | 任务列表（按 app_id 过滤） |
| 日志查看 | `/admin/cmspro/localservice/logs` | 日志列表（按 level 过滤） |
| 拓展管理 | `/admin/cmspro/localservice/extensions` | 拓展列表、配置、测试 |
| 指令管理 | `/admin/cmspro/localservice/scripts` | 指令库 CRUD、执行 |
| 基本配置 | `/admin/cmspro/localservice/settings` | Tab 布局：基本配置（端口、Token、日志级别、日志保留天数、时区）、开放 API、服务命令 |

> **设置页 Tab 结构**：「基本配置」Tab 含保存设置与生成安装/卸载脚本入口；「开放 API」Tab 含启用开关与密钥管理；「服务命令」Tab 展示当前环境检测信息（运行平台、服务名、PHP 运行用户、exec/shell_exec 是否可用、脚本目录）与平台对应的手动管理命令（Windows 为 `sc` 命令，Linux 为 `systemctl` 命令及 PHP 用户 sudo 免密授权一次性配置），每条命令带一键复制按钮——适用于 PHP 禁用 `exec/shell_exec`（宝塔等面板常见）或运行用户权限不足、后台启停按钮无法直接执行的场景，命令内容与后端 `runServiceCommand()` 降级提示保持一致。

### 3.3 服务状态验证

在「仪表盘」页面查看服务状态，确认 Go 服务在线（HTTP 可达且 code=0）。「服务状态」卡片中的版本号为 Go 服务上报的真实版本号（编译时经 ldflags 注入 `buildinfo.Version`）。

> **未安装引导**：服务未安装（本地无二进制且不在线）时，仪表盘状态显示「未安装」，启停按钮禁用，并在卡片内提示前往「基本配置」页面生成安装脚本、按提示以管理员/root 权限运行完成安装；服务上线后提示自动消失并补拉任务数据。最近任务数据存于 Go 服务端，服务离线/未安装时渲染空表格，不再弹「获取任务失败」错误。

### 3.4 服务在线更新

当版本服务器（`https://v5.cmspro.cn`，由 Versionsvr 应用提供）发布了更新的本地服务版本时，「服务状态」卡片右侧自动显示「更新到 x.y.z」按钮：

1. 点击按钮弹出确认框，展示当前版本、新版本号与更新日志
2. 确认后进入**分段状态机执行**（避免单请求长耗时被网关超时中断 502）：
   - `POST /service/update/start` 创建更新任务，返回 `run_id` 与任务信息
   - 前端以 600ms 间隔循环调用 `POST /service/update/step` 推进步骤，弹窗实时显示进度（下载百分比/字节数、替换中、启动验证轮次）
   - `GET /service/update/status` 读取任务状态，页面刷新后自动恢复轮询，不中断更新
3. 步骤流转：`download`（单时间片下载，超时自动断点续传，完成后大小 + SHA256 校验）→ `replace`（停止服务 → 备份旧二进制 → 替换 → 发起启动）→ `verify`（单请求一轮健康探测，最多 5 轮）→ `finished` / `failed` 终态
4. 新版本启动失败时自动回滚旧版本并重启，保证服务可用
5. 服务离线时仅替换二进制文件并提示手动启动，不做停启操作
6. 已有进行中的任务（60 秒内有心跳）时再次发起会返回 50014 并携带现有进度，防双跑

> 平台自动识别：Windows amd64 / Linux amd64 / Linux arm64（按 `php_uname('m')` 判定）。更新耗时与二进制下载速度相关，每个分段请求最长约 15-20 秒，中途关闭弹窗/刷新页面不影响后台任务推进（重新打开页面会恢复轮询）。

**PHP disable_functions 环境自动降级**：当 PHP 环境禁用 `exec/shell_exec`（宝塔等面板常见）或权限不足时，停服/启服命令自动降级**经本地 Go 服务执行**（Go 以系统服务身份运行，不受 PHP 函数禁用限制且权限更高）。因停止命令执行后 Go 进程随之退出，降级停止前会先布置「延迟启动」分离进程——Windows 用延迟启动脚本（`delayed-start.bat`，10 秒后 `sc start`），Linux 用 `systemd-run` 一次性 transient timer（`--on-active=10 --timer-property=AccuracySec=1s`，10 秒后 `systemctl start`），为文件替换留出时间窗口，启动验证轮询（约 20 秒）恰好覆盖——整个更新流程无需人工干预。仪表盘手动「停止」不布置延迟启动，服务保持停止状态。

> Linux 注意一：降级停止执行的是 `systemctl stop` 停止 Go 服务自身，Go 进程被 systemd SIGTERM 终止时 HTTP 响应必然中断（客户端报 `cURL error 52: Empty reply from server`），与「经 Go 重启」同属预期现象。PHP 端以「服务已下线」轮询（短超时探测，窗口小于延迟启动的 10 秒等待期）判定停止成功，不会误报「停止服务失败」而中止更新；停止命令响应正常时以 systemctl 退出码判定（如 unit 不存在的报错会提前失败并给出原始输出）。

> Linux 注意二：延迟启动必须经 `systemd-run` 一次性 timer 布置，不能用 `nohup` 后台脚本——后者是 Go 服务的子进程、归属服务 cgroup，`systemctl stop` 默认 `KillMode=control-group` 会将其连带 SIGTERM 清理，导致延迟启动静默失效、新版本永不拉起、verify 5 轮后误回滚（v1.2.6 及之前版本的实际缺陷）；`systemd-run` 的 timer 由 PID 1 管理，不在服务 cgroup 内，不受停止清理影响。timer 触发精度须用 `--timer-property=AccuracySec=1s` 指定（systemd-run 无 `--accuracy` 选项，误用会导致布置命令退出码 125 而中止更新）。

### 3.5 命令行管理

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
- 修改 Token 或端口后，需重启 Go 服务生效（保存「基本配置」或重新生成 Token 时会自动同步 Go 配置文件，但运行中的 Go 进程需重启才会重新加载）

### Q6：卸载重装应用后服务显示离线/无法连接？

- 根因：卸载重装后 PHP 端 Token 已重新生成，而运行中的 Go 服务进程仍持有旧 Token，双方认证失败（HTTP 401），后台即显示服务离线
- 处理：在「基本配置」页面重新保存设置或重新生成 Token（会自动同步 Go 配置文件），然后重启 Go 服务（Windows 管理员执行 `sc stop CmsProLocalService && sc start CmsProLocalService`；Linux 执行 `systemctl restart CmsProLocalService`）
- v1.1.5 起安装/升级时自动同步 Go 配置文件，重装后只需重启 Go 服务即可恢复

---

## 七、版本历史

| 版本 | 日期 | 说明 |
|------|------|------|
| 1.0.0 | 2026-08-13 | 初始版本，基于应用 v1.0.1 编写 |
| 1.1.0 | 2026-09-18 | 新增 AI 透传扩展（llmrelay）：Go 服务提供通用 LLM SSE 透传端点 `/open/llm/{app}/{prefix}/*path` 与健康检查 `/open/llm/ping`，Go 侧新增 `cmspro_base_url` 配置（回调 CmsPro 基础地址，随 `syncConfig()` 同步）；决策契约支持 `local_response` 本地短路（管理类端点如 models 由 CmsPro 侧本地出数，不透传上游）；接入契约与使用方法见《CmsPro-本地服务-AI透传扩展指南》 |
| 1.1.1 | 2026-09-18 | 透传契约新增 `body_override` 请求体改写：决策请求随 `body` 上送客户端原始请求体，决策响应下发 `body_override` 时 Go 整体替换原始请求体转发上游（如应用侧模型别名解析为真实 model_id）；换号重试时决策始终基于原始请求体，改写不污染后续决策 |
| 1.1.2 | 2026-09-18 | Go 透传故障无感切换增强：上游连接失败、首字节超时、空闲超时、流中断等连接层失败在未向客户端输出实质数据前自动换号重试（此前直接终止）；心跳注释不再重置空闲计时，修复上游挂死时半开连接永不判定导致客户端长时间收不到数据自行断开的问题 |
| 1.1.3 | 2026-09-18 | PHP 侧 disable_functions 兼容修复：宝塔等面板禁用 shell_exec 时生成安装/卸载脚本报 500 的问题——二进制架构检测改用 php_uname() 纯 PHP 实现；服务启停等 shell 调用统一经 function_exists 安全封装，函数被禁时返回明确业务提示而非致命错误 |
| 1.1.4 | 2026-09-18 | Linux 安装/卸载脚本兼容 systemd 目录 immutable 加固：宝塔等面板对 `/etc/systemd/system` 执行 `chattr +i` 后，root 写入/删除 unit 文件同样报 `Operation not permitted`；脚本现自动检测加固并临时解除、操作完成后恢复，同时处理已存在 unit 文件被加固导致覆盖或删除失败的情况，`chattr`/`lsattr` 缺失时静默降级 |
| 1.1.5 | 2026-09-18 | 修复卸载重装后服务显示离线的问题：重装后 PHP 端 Token 重新生成，但 Go 配置文件与运行中进程仍持旧 Token 导致认证 401；现安装/升级时自动同步 Go 配置文件，「基本配置」保存、重新生成 Token/开放 API 密钥后也自动同步（此前保存操作不会同步，文档描述与实现不符） |
| 1.1.6 | 2026-09-20 | 修复 Go 透传通道高频报「上游连接失败: http2: timeout awaiting response headers」的问题：等响应头预算此前误用 first_byte_timeout（10s/30s），大上下文 prefill 超过即失败；现决策契约新增 `header_timeout_ms`（对齐 PHP 通道 header_timeout=60s），first_byte_timeout 回归「响应头后首数据空读上限」语义。需各接入应用同步升级后生效 |
| 1.2.0 | 2026-09-24 | 服务在线更新：Go 服务真实版本号经 ldflags 注入（`buildinfo.Version`），仪表盘「服务状态」显示真实版本；新增 UpdateService 对接版本服务器（v5.cmspro.cn）实现检查更新与应用更新（下载校验→停服→备份替换→重启→失败回滚），有新版本时仪表盘显示「更新」按钮一键升级。配套 Versionsvr 应用 v1.1.0 的「本地服务」版本发布功能使用 |
| 1.2.1 | 2026-09-25 | 更新执行模式重构为分段状态机（修复一键更新 502）：原 apply 接口在单 HTTP 请求内同步完成下载→停服→替换→重启，大文件下载时被 Nginx/PHP-FPM 超时中断（502 Bad Gateway）；现拆分为 `update/start`（创建任务返回 run_id）+ `update/step`（前端 600ms 轮询单步推进）+ `update/status`（页面刷新恢复轮询）三接口，下载支持 15 秒时间片 + Range 断点续传，新增进行中任务防双跑（50014）与任务失效（50015）错误码 |
| 1.2.2 | 2026-10-10 | 安装脚本版本来源切换与未安装引导：生成安装脚本时二进制自动同步为版本服务器（Versionsvr）最新已发布版本（latest 接口下发下载地址 + 大小/SHA256 校验，替代旧硬编码静态地址；本地落后或无版本标记时自动重下，服务器不可达时降级用本地文件），版本经 `Bin/local-service.version` 标记比对；Linux arm64 机器安装脚本改用 `currentBinaryPath()` 修正此前引用不存在 amd64 路径的问题；仪表盘新增「未安装」状态——无二进制且不在线时提示前往设置页生成安装脚本，启停按钮禁用，离线期间最近任务渲染空表格不再弹错 |
| 1.2.3 | 2026-10-10 | 设置页 Tab 化与服务命令面板：设置页重构为 layui Tab 布局（基本配置 / 开放 API / 服务命令）；新增 `GET /api/admin/cmspro/localservice/service/commands` 接口（`ServiceManager::serviceCommands()`），返回运行平台、服务名、PHP 运行用户、exec/shell_exec 可用性、脚本目录及平台对应手动管理命令清单（Windows `sc` / Linux `systemctl` + PHP 用户 sudo 免密授权一次性配置），前端按分组渲染并支持一键复制——解决宝塔等面板禁用 `exec/shell_exec` 或运行用户权限不足时后台启停按钮失效、用户无从获取命令的问题，命令内容与 `runServiceCommand()` 降级提示保持一致 |
| 1.2.4 | 2026-10-10 | 修复启停按钮"静默成功"误报为"exec/shell_exec 均不可用"：`safeShellExec()` 此前以"有输出"作为命令可执行依据，而 Linux `sudo systemctl start/stop/restart` 成功时静默无输出——配置 sudo 免密授权后命令实际执行成功，却因空输出被误判为函数不可用（503），且重启实际已生效；现 `safeShellExec()` 改为返回 `output + exit_code` 结构（优先 exec 获取可靠退出码，shell_exec 兜底时以 -1 标记），`runServiceCommand()` 以退出码判定成败（退出码未知时按输出特征兜底），空输出 + 退出码非失败 = 成功；Windows restart 分支同步适配，503 降级提示统一引导至设置页「服务命令」Tab |
| 1.2.5 | 2026-10-10 | Linux 重启补齐经 Go 服务的降级路径：`exec/shell_exec` 被面板禁用（disable_functions）时 sudoers 授权无法绕过函数禁用，此前 stop 有「经本地 Go 服务执行」降级而 restart 没有——PHP 直接执行必然 503；现 `restart()` 在 403/503 时走新增的 `restartViaGo()`：经 Go 执行 `systemctl restart`（Go 自身随服务重启退出、响应中断属预期），随后以 2 秒短超时轮询 status（最多 8 次，unit `RestartSec=5` 约 6-10 秒内恢复）确认服务重新上线；`LocalServiceClient::isOnline()` 新增可选超时参数支持短超时探测。注：启动（start）场景服务停止后 Go 必然离线，无法经 Go 降级，仅能引导至「服务命令」Tab 手动执行 |
| 1.2.6 | 2026-10-14 | 修复 Linux 在线更新报「停止服务失败：经本地服务执行停止命令失败：本地服务不可用： cURL error 52」而中止更新的问题：降级停止经 Go 执行 `systemctl stop` 停止 Go 服务自身，进程被 systemd SIGTERM 终止时 HTTP 响应必然中断（cURL error 52），此前被误判为停止失败——实际服务已停止、延迟启动已布置；现响应中断时以「服务已下线」短超时轮询（`waitServiceOffline()`，窗口小于延迟启动 10 秒等待期）判定停止成功，仅在下线确认失败时报错；响应正常时补查 systemctl 退出码（unit 不存在等报错提前失败并给出原始输出），Windows 分支行为不变 |
| 1.2.7 | 2026-10-14 | 修复 Linux 在线更新报「新版本启动失败，已回滚旧版本」的问题（journal 无任何 start 记录实证延迟启动从未执行）：延迟启动此前用 `nohup bash delayed-start.sh &` 布置，该后台进程是 Go 服务的子进程、归属服务 cgroup，紧随其后的 `systemctl stop` 默认 `KillMode=control-group` 将其连带 SIGTERM 清理——延迟启动静默失效，新版本永不拉起，verify 5 轮后误回滚；现 Linux 改用 `systemd-run` 一次性 transient timer 布置延迟启动（timer 由 PID 1 管理，不在服务 cgroup 内，不受停止清理影响），Windows 仍用 `delayed-start.bat`（`createDelayedStartScript()` 清理为仅 Windows 逻辑），并已在 WSL/deepin25 实测 systemd-run 延迟启动链路可用 |
| 1.2.8 | 2026-10-14 | 修复 v1.2.7 的 systemd-run 精度参数误用：`--accuracy=1s` 不是 systemd-run 的有效选项（WSL 实测报 `unrecognized option`、退出码 125），导致「布置延迟启动进程失败：success」中止更新；改用 `--timer-property=AccuracySec=1s` 指定触发精度（实测 5 秒 timer 精确触发），同时修复布置失败时错误信息被 message（success）掩盖的问题——命令层面失败现给出「退出码 + 原始输出」，便于快速定位 |
