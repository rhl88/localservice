<?php

namespace App\Apps\CmsproLocalservice\Services;

use App\Apps\CmsproLocalservice\Models\McpKey;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\Str;

/**
 * MCP 服务管理。
 *
 * 职责：
 * 1. 工具清单与运行时配置的获取（优先取 Go 服务 /api/mcp/tools、/api/mcp/config，失败回落内置快照）
 * 2. 凭据的增删改查与权限矩阵规范化（低风险默认授权、中高风险 opt-in）
 * 3. 访问控制配置（开关、路由前缀、IP 白名单、限流）的读写
 * 4. 配置下发：把 MCP 配置写入 local_service_config.json，Go 服务读取后生效
 *
 * 设计原则：配置真源在 PHP（数据库 + 配置文件），Go 侧只读使用，
 * 保证「后台单一入口管理」与宝塔 MCP 一致的使用体验。
 */
class McpService
{
    /** 配置项 code 前缀（与 manifest 配置组一致） */
    private const CONFIG_PREFIX = 'app_cmspro_localservice_';

    /** MCP 相关配置项键名 */
    public const ITEM_ENABLED = 'mcp_enabled';

    public const ITEM_PATH_PREFIX = 'mcp_path_prefix';

    public const ITEM_IP_ALLOWLIST = 'mcp_ip_allowlist';

    public const ITEM_RATE_MAX = 'mcp_rate_limit_max';

    public const ITEM_RATE_WINDOW = 'mcp_rate_limit_window';

    /**
     * 内置工具快照（Go 服务不可用时兜底展示，与 internal/mcp/tools.go 保持同步）
     *
     * 结构：tool_id => [name, risk_level, category, confirm, description]
     *
     * @return array<string, array<string, mixed>>
     */
    public function builtinToolSnapshot(): array
    {
        return [
            // 系统
            'SystemInfo' => ['name' => '获取系统信息', 'risk' => 'low', 'category' => '系统', 'confirm' => false],
            'SystemNetwork' => ['name' => '获取网络信息', 'risk' => 'low', 'category' => '系统', 'confirm' => false],
            'SystemExec' => ['name' => '执行系统命令', 'risk' => 'high', 'category' => '系统', 'confirm' => true],
            'SystemScript' => ['name' => '执行脚本', 'risk' => 'high', 'category' => '系统', 'confirm' => true],
            // 文件
            'FileRead' => ['name' => '读取文件', 'risk' => 'low', 'category' => '文件', 'confirm' => false],
            'FileList' => ['name' => '列出目录', 'risk' => 'low', 'category' => '文件', 'confirm' => false],
            'FileExists' => ['name' => '判断文件是否存在', 'risk' => 'low', 'category' => '文件', 'confirm' => false],
            'FileInfo' => ['name' => '获取文件信息', 'risk' => 'low', 'category' => '文件', 'confirm' => false],
            'FileWrite' => ['name' => '写入文件', 'risk' => 'high', 'category' => '文件', 'confirm' => true],
            'FileDelete' => ['name' => '删除文件', 'risk' => 'high', 'category' => '文件', 'confirm' => true],
            'FileMove' => ['name' => '移动/重命名文件', 'risk' => 'high', 'category' => '文件', 'confirm' => true],
            'FileCopy' => ['name' => '复制文件', 'risk' => 'medium', 'category' => '文件', 'confirm' => false],
            'FileMkdir' => ['name' => '创建目录', 'risk' => 'medium', 'category' => '文件', 'confirm' => false],
            // 进程
            'ProcessList' => ['name' => '列出进程', 'risk' => 'low', 'category' => '进程', 'confirm' => false],
            'ProcessStart' => ['name' => '启动进程', 'risk' => 'high', 'category' => '进程', 'confirm' => true],
            'ProcessKill' => ['name' => '终止进程', 'risk' => 'high', 'category' => '进程', 'confirm' => true],
            'ProcessKillByName' => ['name' => '按名称终止进程', 'risk' => 'high', 'category' => '进程', 'confirm' => true],
            // 服务
            'ServiceList' => ['name' => '列出系统服务', 'risk' => 'low', 'category' => '服务', 'confirm' => false],
            'ServiceStatus' => ['name' => '查询服务状态', 'risk' => 'low', 'category' => '服务', 'confirm' => false],
            'ServiceStart' => ['name' => '启动服务', 'risk' => 'medium', 'category' => '服务', 'confirm' => false],
            'ServiceStop' => ['name' => '停止服务', 'risk' => 'medium', 'category' => '服务', 'confirm' => false],
            // 浏览器
            'BrowserOpen' => ['name' => '打开浏览器', 'risk' => 'medium', 'category' => '浏览器', 'confirm' => false],
            'BrowserClose' => ['name' => '关闭浏览器', 'risk' => 'medium', 'category' => '浏览器', 'confirm' => false],
            'BrowserNavigate' => ['name' => '浏览器跳转', 'risk' => 'medium', 'category' => '浏览器', 'confirm' => false],
            'BrowserScreenshot' => ['name' => '页面截图', 'risk' => 'low', 'category' => '浏览器', 'confirm' => false],
            'BrowserExecute' => ['name' => '执行页面脚本', 'risk' => 'high', 'category' => '浏览器', 'confirm' => true],
            'BrowserGetCookies' => ['name' => '获取 Cookie', 'risk' => 'low', 'category' => '浏览器', 'confirm' => false],
            'BrowserSetCookies' => ['name' => '设置 Cookie', 'risk' => 'medium', 'category' => '浏览器', 'confirm' => false],
            'BrowserSetHeaders' => ['name' => '设置请求头', 'risk' => 'medium', 'category' => '浏览器', 'confirm' => false],
            'BrowserClick' => ['name' => '点击页面元素', 'risk' => 'medium', 'category' => '浏览器', 'confirm' => false],
            'BrowserInput' => ['name' => '输入文本', 'risk' => 'medium', 'category' => '浏览器', 'confirm' => false],
            'BrowserWaitFor' => ['name' => '等待页面元素', 'risk' => 'low', 'category' => '浏览器', 'confirm' => false],
        ];
    }

    /**
     * 获取工具清单（含风险统计与分类分组）。
     *
     * 优先从 Go 服务实时获取（保证与 Go 侧注册表一致）；不可用时回落内置快照，
     * 使后台在服务离线时仍可查看与配置权限。
     *
     * @return array{tools: array<int, array<string, mixed>>, total: int, risk_counts: array<string, int>, source: string}
     */
    public function tools(): array
    {
        $remote = $this->fetchRemoteTools();
        if ($remote !== null) {
            return $remote;
        }

        // 回落内置快照
        $tools = [];
        $riskCounts = ['low' => 0, 'medium' => 0, 'high' => 0];
        foreach ($this->builtinToolSnapshot() as $toolId => $meta) {
            $risk = (string) $meta['risk'];
            $riskCounts[$risk] = ($riskCounts[$risk] ?? 0) + 1;
            $tools[] = [
                'tool_id' => $toolId,
                'name' => (string) $meta['name'],
                'risk_level' => $risk,
                'category' => (string) $meta['category'],
                'confirm' => (bool) $meta['confirm'],
                'description' => '',
            ];
        }

        return [
            'tools' => $tools,
            'total' => count($tools),
            'risk_counts' => $riskCounts,
            'source' => 'snapshot',
        ];
    }

    /**
     * 从 Go 服务获取工具清单（失败返回 null）
     *
     * @return array{tools: array<int, array<string, mixed>>, total: int, risk_counts: array<string, int>, source: string}|null
     */
    protected function fetchRemoteTools(): ?array
    {
        try {
            $response = Http::baseUrl(app(ServiceManager::class)->relayBaseUrl())
                ->timeout(5)
                ->withToken((string) $this->configItem('auth_token', ''))
                ->get('/api/mcp/tools');

            if (! $response->successful()) {
                return null;
            }
            $json = $response->json();
            $data = $json['data'] ?? null;
            if (! is_array($data) || ! isset($data['tools'])) {
                return null;
            }

            // 归一化：Go 返回 name/task_type/input_schema 等，此处保留界面所需字段
            $tools = [];
            foreach ($data['tools'] as $tool) {
                if (! is_array($tool) || empty($tool['tool_id'])) {
                    continue;
                }
                $tools[] = [
                    'tool_id' => (string) $tool['tool_id'],
                    'name' => (string) ($tool['name'] ?? $tool['tool_id']),
                    'risk_level' => (string) ($tool['risk_level'] ?? 'low'),
                    'category' => (string) ($tool['category'] ?? '其他'),
                    'confirm' => (bool) ($tool['confirm'] ?? false),
                    'description' => (string) ($tool['description'] ?? ''),
                ];
            }

            return [
                'tools' => $tools,
                'total' => (int) ($data['total'] ?? count($tools)),
                'risk_counts' => is_array($data['risk_counts'] ?? null) ? $data['risk_counts'] : $this->countRisk($tools),
                'source' => 'service',
            ];
        } catch (\Throwable $e) {
            Log::debug('获取 MCP 工具清单失败，回落内置快照: '.$e->getMessage());

            return null;
        }
    }

    /**
     * 统计各风险等级工具数
     *
     * @param  array<int, array<string, mixed>>  $tools
     * @return array<string, int>
     */
    protected function countRisk(array $tools): array
    {
        $counts = ['low' => 0, 'medium' => 0, 'high' => 0];
        foreach ($tools as $tool) {
            $risk = (string) ($tool['risk_level'] ?? 'low');
            $counts[$risk] = ($counts[$risk] ?? 0) + 1;
        }

        return $counts;
    }

    /**
     * 已注册工具标识列表（用于权限分区）
     *
     * @return array<int, string>
     */
    public function registeredToolIds(): array
    {
        return array_map(
            static fn (array $tool): string => (string) $tool['tool_id'],
            $this->tools()['tools']
        );
    }

    /**
     * 低风险工具标识（新建凭据的默认授权集）
     *
     * @return array<int, string>
     */
    public function lowRiskToolIds(): array
    {
        $out = [];
        foreach ($this->tools()['tools'] as $tool) {
            if (($tool['risk_level'] ?? '') === 'low') {
                $out[] = (string) $tool['tool_id'];
            }
        }

        return $out;
    }

    /**
     * 读取 MCP 访问控制配置
     *
     * @return array{enabled: bool, path_prefix: string, ip_allowlist: array<int, string>, rate_limit_max: int, rate_limit_window: int, mcp_path: string}
     */
    public function config(): array
    {
        $pathPrefix = (string) $this->configItem(self::ITEM_PATH_PREFIX, '');
        if ($pathPrefix === '') {
            // 首次读取时生成并持久化不可预测前缀（防扫描）
            $pathPrefix = $this->regeneratePathPrefix();
        }

        $allowlist = (string) $this->configItem(self::ITEM_IP_ALLOWLIST, '*');

        return [
            'enabled' => (int) $this->configItem(self::ITEM_ENABLED, 0) === 1,
            'path_prefix' => $pathPrefix,
            'ip_allowlist' => $this->parseAllowlist($allowlist),
            'rate_limit_max' => (int) $this->configItem(self::ITEM_RATE_MAX, 300),
            'rate_limit_window' => (int) $this->configItem(self::ITEM_RATE_WINDOW, 60),
            'mcp_path' => $pathPrefix.'/mcp',
        ];
    }

    /**
     * 保存访问控制配置并下发
     *
     * @param  array<string, mixed>  $payload
     */
    public function saveConfig(array $payload): void
    {
        if (array_key_exists('enabled', $payload)) {
            $this->writeConfigItem(self::ITEM_ENABLED, $payload['enabled'] ? '1' : '0');
        }
        if (array_key_exists('ip_allowlist', $payload)) {
            $list = is_array($payload['ip_allowlist']) ? $payload['ip_allowlist'] : [];
            $this->writeConfigItem(self::ITEM_IP_ALLOWLIST, implode(',', $list));
        }
        if (array_key_exists('rate_limit_max', $payload)) {
            $this->writeConfigItem(self::ITEM_RATE_MAX, (string) max(1, (int) $payload['rate_limit_max']));
        }
        if (array_key_exists('rate_limit_window', $payload)) {
            $this->writeConfigItem(self::ITEM_RATE_WINDOW, (string) max(1, (int) $payload['rate_limit_window']));
        }
    }

    /**
     * 重新生成路由前缀（不可预测，防端口扫描）
     */
    public function regeneratePathPrefix(): string
    {
        $prefix = '/mcp-'.Str::random(12);
        $this->writeConfigItem(self::ITEM_PATH_PREFIX, $prefix);

        return $prefix;
    }

    /**
     * 解析 IP 白名单字符串（逗号分隔）为数组
     *
     * @return array<int, string>
     */
    protected function parseAllowlist(string $raw): array
    {
        $items = array_filter(array_map('trim', explode(',', $raw)), static fn ($v) => $v !== '');
        if ($items === []) {
            return ['*'];
        }

        return array_values($items);
    }

    /**
     * 校验 IP 白名单条目（支持 *、IP、CIDR、A-B 区间）
     */
    public function validateAllowEntry(string $entry): bool
    {
        $entry = trim($entry);
        if ($entry === '') {
            return false;
        }
        if ($entry === '*') {
            return true;
        }
        if (str_contains($entry, '-')) {
            [$start, $end] = array_pad(explode('-', $entry, 2), 2, '');
            $startIp = ip2long(trim($start));
            $endIp = ip2long(trim($end));

            return $startIp !== false && $endIp !== false && $startIp <= $endIp;
        }
        if (str_contains($entry, '/')) {
            [$ip, $mask] = array_pad(explode('/', $entry, 2), 2, '');
            $maskNum = (int) $mask;

            return filter_var($ip, FILTER_VALIDATE_IP) !== false && $maskNum >= 0 && $maskNum <= 32;
        }

        return filter_var($entry, FILTER_VALIDATE_IP) !== false;
    }

    // ============ 凭据管理 ============

    /**
     * 凭据列表（附生效工具数与掩码令牌）
     *
     * @return array<int, array<string, mixed>>
     */
    public function keys(): array
    {
        $registered = $this->registeredToolIds();

        return McpKey::orderByDesc('id')->get()->map(function (McpKey $key) use ($registered): array {
            return $this->formatKey($key, $registered);
        })->all();
    }

    /**
     * 凭据详情（含明文令牌，仅在创建后与显式查看时返回）
     *
     * @return array<string, mixed>|null
     */
    public function keyDetail(int $id, bool $withToken = false): ?array
    {
        $key = McpKey::find($id);
        if ($key === null) {
            return null;
        }

        $data = $this->formatKey($key, $this->registeredToolIds());
        if ($withToken) {
            $data['token'] = (string) $key->token;
        }

        return $data;
    }

    /**
     * 创建凭据。
     *
     * 未显式指定工具时按「低风险默认授权、中高风险 opt-in」赋默认值。
     *
     * @param  array<string, mixed>  $payload
     */
    public function createKey(array $payload): McpKey
    {
        $registered = $this->registeredToolIds();
        $granted = isset($payload['tool_ids']) && is_array($payload['tool_ids'])
            ? $payload['tool_ids']
            : $this->lowRiskToolIds();

        [$normalized, $disabled] = McpKey::partitionTools($granted, $registered);

        $key = McpKey::create([
            'key_id' => McpKey::generateKeyId(),
            'name' => trim((string) ($payload['name'] ?? '未命名凭据')) ?: '未命名凭据',
            'token' => McpKey::generateToken(),
            'tool_ids' => json_encode($normalized, JSON_UNESCAPED_UNICODE),
            'disabled' => json_encode($disabled, JSON_UNESCAPED_UNICODE),
            'expires_days' => (int) ($payload['expires_days'] ?? 7),
            'active' => 1,
        ]);

        $this->syncConfig();

        return $key;
    }

    /**
     * 更新凭据（名称、有效期、启停、授权工具集）
     *
     * @param  array<string, mixed>  $payload
     */
    public function updateKey(int $id, array $payload): ?McpKey
    {
        $key = McpKey::find($id);
        if ($key === null) {
            return null;
        }

        if (array_key_exists('name', $payload)) {
            $name = trim((string) $payload['name']);
            if ($name !== '') {
                $key->name = $name;
            }
        }
        if (array_key_exists('expires_days', $payload)) {
            $key->expires_days = (int) $payload['expires_days'];
        }
        if (array_key_exists('active', $payload)) {
            $key->active = ! empty($payload['active']) ? 1 : 0;
        }
        if (array_key_exists('tool_ids', $payload) && is_array($payload['tool_ids'])) {
            [$normalized, $disabled] = McpKey::partitionTools($payload['tool_ids'], $this->registeredToolIds());
            $key->tool_ids = json_encode($normalized, JSON_UNESCAPED_UNICODE);
            $key->disabled = json_encode($disabled, JSON_UNESCAPED_UNICODE);
        }

        $key->save();
        $this->syncConfig();

        return $key;
    }

    /**
     * 删除凭据
     */
    public function deleteKey(int $id): bool
    {
        $key = McpKey::find($id);
        if ($key === null) {
            return false;
        }
        $key->delete();
        $this->syncConfig();

        return true;
    }

    /**
     * 仅授权低风险工具（一键操作，与宝塔「仅授权低风险」一致）
     */
    public function grantLowRiskOnly(int $id): ?McpKey
    {
        return $this->updateKey($id, ['tool_ids' => $this->lowRiskToolIds()]);
    }

    /**
     * 恢复默认授权（等价于仅授权低风险）
     */
    public function restoreDefault(int $id): ?McpKey
    {
        return $this->grantLowRiskOnly($id);
    }

    /**
     * 格式化凭据输出
     *
     * @param  array<int, string>  $registered
     * @return array<string, mixed>
     */
    protected function formatKey(McpKey $key, array $registered): array
    {
        $effective = $key->effectiveTools($registered);

        return [
            'id' => $key->id,
            'key_id' => (string) $key->key_id,
            'name' => (string) $key->name,
            'token_masked' => $key->maskedToken(),
            'tool_ids' => $key->grantedTools(),
            'disabled' => $key->disabledTools(),
            'granted_count' => count($effective),
            'total_count' => count($registered),
            'expires_days' => (int) $key->expires_days,
            'expires_label' => McpKey::EXPIRES_OPTIONS[(int) $key->expires_days] ?? ((int) $key->expires_days.' 天'),
            'active' => (int) $key->active,
            'expired' => $key->isExpired(),
            'effective' => $key->isEffective(),
            'create_time' => $key->create_time?->format('Y-m-d H:i:s'),
        ];
    }

    // ============ 配置下发 ============

    /**
     * 把 MCP 配置写入 Go 服务配置文件并触发热重载（免重启生效）。
     *
     * 凭据以明文 Token 下发——Go 服务与本配置文件同在受保护的 storage 目录，
     * 且服务仅监听 127.0.0.1；令牌泄露风险由部署隔离与文件权限控制。
     *
     * 热重载失败不视为失败（配置已落盘，服务重启后仍会生效）——旧版本 Go 服务
     * 无 /api/mcp/reload 端点时静默降级为「重启生效」。
     */
    public function syncConfig(): string
    {
        $path = app(ServiceManager::class)->syncConfig();
        $this->triggerHotReload();

        return $path;
    }

    /**
     * 触发 Go 服务配置热重载
     */
    protected function triggerHotReload(): bool
    {
        try {
            $response = Http::baseUrl(app(ServiceManager::class)->relayBaseUrl())
                ->withToken((string) $this->configItem('auth_token', ''))
                ->timeout(5)
                ->post('/api/mcp/reload');

            if ($response->successful() && ($response->json('data.reloaded') ?? false)) {
                return true;
            }

            Log::debug('MCP 配置热重载未成功（将随服务重启生效）', [
                'status' => $response->status(),
                'body' => mb_substr($response->body(), 0, 200),
            ]);
        } catch (\Throwable $e) {
            Log::debug('MCP 配置热重载请求失败（将随服务重启生效）: '.$e->getMessage());
        }

        return false;
    }

    /**
     * 构造下发给 Go 服务的 MCP 配置段
     *
     * @return array<string, mixed>
     */
    public function buildGoConfig(): array
    {
        $config = $this->config();
        $registered = $this->registeredToolIds();

        $credentials = [];
        foreach (McpKey::orderBy('id')->get() as $key) {
            $credentials[] = [
                'id' => (string) $key->key_id,
                'token' => (string) $key->token,
                'name' => (string) $key->name,
                'tool_ids' => $key->effectiveTools($registered),
                'disabled' => $key->disabledTools(),
                'created_at' => $key->create_time?->format('Y-m-d H:i:s') ?? '',
                'expires_days' => (int) $key->expires_days,
                'active' => (int) $key->active === 1,
            ];
        }

        return [
            'enabled' => $config['enabled'],
            'path_prefix' => $config['path_prefix'],
            'ip_allowlist' => $config['ip_allowlist'],
            'rate_limit_max' => $config['rate_limit_max'],
            'rate_limit_window' => $config['rate_limit_window'],
            'credentials' => $credentials,
            'default_timeout' => 40,
            'max_concurrent' => 12,
        ];
    }

    /**
     * 生成给 AI Agent 的接入提示词（对齐宝塔「复制提示词」体验）
     */
    public function buildPrompt(): string
    {
        $config = $this->config();
        $key = McpKey::where('active', 1)->orderByDesc('id')->first();

        $url = rtrim((string) config('app.url', 'http://127.0.0.1'), '/');
        $endpoint = $url.':'.(string) $this->configItem('port', 9501).$config['mcp_path'];
        $token = $key !== null ? (string) $key->token : '<请先在后台创建访问凭据>';

        return <<<PROMPT
请为我接入 CmsPro 本地服务 MCP，配置如下：

{
  "mcpServers": {
    "cmspro-local": {
      "url": "{$endpoint}",
      "headers": {
        "Authorization": "Bearer {$token}"
      },
      "disabled": false
    }
  }
}

说明：
1. 该 MCP 服务提供本机（运行本地服务的机器）的系统、文件、进程、服务与浏览器操作能力。
2. 高风险工具（执行命令、写删文件、终止进程等）首次调用会返回 waiting_confirm，需要用户明确确认后才会执行。
3. 接入后请先用只读工具（如 SystemInfo）验证连通性，确认正常后再执行其他操作。
PROMPT;
    }

    /**
     * 读取配置项值
     */
    protected function configItem(string $name, mixed $default = null): mixed
    {
        try {
            $value = DB::table('config_items')
                ->where('code', self::CONFIG_PREFIX.$name)
                ->value('value');
        } catch (\Throwable $e) {
            return $default;
        }

        return $value ?? $default;
    }

    /**
     * 写入配置项值（不存在时按 manifest 结构补建到「服务设置」配置组）
     */
    protected function writeConfigItem(string $name, string $value): void
    {
        $code = self::CONFIG_PREFIX.$name;
        $item = null;
        try {
            $item = DB::table('config_items')->where('code', $code)->first();
        } catch (\Throwable $e) {
            return;
        }

        if ($item !== null) {
            DB::table('config_items')->where('code', $code)->update(['value' => $value]);

            return;
        }

        // 补建：取服务设置配置组 ID
        $groupId = 0;
        try {
            $groupId = (int) DB::table('config_groups')
                ->where('code', self::CONFIG_PREFIX.'service_settings')
                ->value('id');
        } catch (\Throwable $e) {
            $groupId = 0;
        }
        if ($groupId <= 0) {
            Log::warning('MCP 配置分组未找到，跳过补建: '.$code);

            return;
        }

        DB::table('config_items')->insert([
            'group_id' => $groupId,
            'name' => 'MCP 服务配置',
            'code' => $code,
            'value' => $value,
            'type' => 'text',
            'sort' => 90,
            'status' => 1,
        ]);
    }
}