<?php

namespace App\Apps\CmsproLocalservice\Tests\Feature;

use App\Apps\CmsproLocalservice\Models\McpKey;
use App\Apps\CmsproLocalservice\Services\McpService;
use App\Apps\CmsproLocalservice\Tests\LocalserviceTestCase;
use Illuminate\Support\Facades\Http;

/**
 * MCP 服务管理测试。
 *
 * 覆盖：工具清单（含 Go 服务不可用时的快照回落）、访问控制配置读写与校验、
 * 凭据 CRUD 与「凭据 × 工具」权限矩阵（低风险默认授权、中高风险 opt-in）、
 * 令牌掩码、有效期、配置下发结构、接入提示词。
 */
class McpApiTest extends LocalserviceTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->actingAdmin();

        $this->seedConfigItems();
    }

    /**
     * 断言工具清单回落内置快照（Go 服务不可用）
     */
    private function fakeServiceUnavailable(): void
    {
        Http::fake(['*' => Http::response('', 500)]);
    }

    /**
     * 预置配置分组与 MCP 配置项。
     *
     * 配置项在正式环境由迁移（add_mcp_config_items）补建；测试库不含框架完整
     * 配置数据，故此处按同等结构预置，使配置读写走真实链路。
     */
    protected function seedConfigItems(): void
    {
        $groupId = \Illuminate\Support\Facades\DB::table('config_groups')->insertGetId([
            'name' => '服务设置',
            'code' => 'app_cmspro_localservice_service_settings',
            'app_id' => 'cmspro.localservice',
            'sort' => 0,
            'status' => 1,
        ]);

        foreach ([
            ['port', '监听端口', '9501', 'number'],
            ['mcp_enabled', 'MCP服务开关', '0', 'switch'],
            ['mcp_path_prefix', 'MCP路由前缀', '', 'text'],
            ['mcp_ip_allowlist', 'MCP IP白名单', '*', 'text'],
            ['mcp_rate_limit_max', 'MCP限流上限', '300', 'number'],
            ['mcp_rate_limit_window', 'MCP限流窗口', '60', 'number'],
        ] as [$suffix, $name, $value, $type]) {
            \Illuminate\Support\Facades\DB::table('config_items')->insert([
                'group_id' => $groupId,
                'name' => $name,
                'code' => 'app_cmspro_localservice_'.$suffix,
                'value' => $value,
                'type' => $type,
                'sort' => 0,
                'status' => 1,
            ]);
        }
    }

    private function url(string $path = ''): string
    {
        return '/api/admin/cmspro/localservice/mcp'.($path !== '' ? '/'.$path : '');
    }

    // ============ 工具清单 ============

    public function test_tools_falls_back_to_snapshot_when_service_unavailable(): void
    {
        $this->fakeServiceUnavailable();

        $response = $this->getJson($this->url('tools'));

        $response->assertOk();
        $response->assertJsonPath('code', 0);
        $response->assertJsonPath('data.source', 'snapshot');

        $data = $response->json('data');
        $this->assertGreaterThan(0, $data['total']);
        $this->assertArrayHasKey('low', $data['risk_counts']);
        $this->assertArrayHasKey('grouped', $data);
        // 快照含系统、文件、进程、服务、浏览器五类
        foreach (['系统', '文件', '进程', '服务', '浏览器'] as $category) {
            $this->assertArrayHasKey($category, $data['grouped'], "缺少分类：{$category}");
        }
    }

    public function test_tools_prefers_service_when_available(): void
    {
        // 覆盖 setUp 的「服务不可用」伪造：重新 fake 使本次请求命中服务端响应
        Http::fake([
            '*/api/mcp/tools' => Http::response([
                'code' => 0,
                'data' => [
                    'tools' => [
                        ['tool_id' => 'CustomTool', 'name' => '自定义工具', 'risk_level' => 'high', 'category' => '自定义', 'confirm' => true, 'description' => 'x'],
                    ],
                    'total' => 1,
                    'risk_counts' => ['low' => 0, 'medium' => 0, 'high' => 1],
                ],
            ], 200),
        ]);

        $response = $this->getJson($this->url('tools'));

        $response->assertOk();
        $response->assertJsonPath('data.source', 'service');
        $response->assertJsonPath('data.total', 1);
        $response->assertJsonPath('data.tools.0.tool_id', 'CustomTool');
    }

    public function test_registered_tool_ids_derived_from_tools(): void
    {
        $ids = app(McpService::class)->registeredToolIds();

        $this->assertContains('SystemInfo', $ids);
        $this->assertContains('SystemExec', $ids);
    }

    public function test_low_risk_tool_ids_excludes_medium_and_high(): void
    {
        $service = app(McpService::class);
        $lowIds = $service->lowRiskToolIds();
        $snapshot = $service->builtinToolSnapshot();

        $this->assertNotEmpty($lowIds);
        foreach ($lowIds as $id) {
            $this->assertSame('low', $snapshot[$id]['risk'] ?? null, "工具 {$id} 不应出现在低风险集");
        }
    }

    // ============ 访问控制配置 ============

    public function test_config_generates_unpredictable_prefix_on_first_read(): void
    {
        $response = $this->getJson($this->url('config'));

        $response->assertOk();
        $prefix = $response->json('data.path_prefix');

        // 前缀形如 /mcp-{12位随机}
        $this->assertMatchesRegularExpression('#^/mcp-[A-Za-z0-9]{12}$#', $prefix);
        $this->assertSame($prefix.'/mcp', $response->json('data.mcp_path'));
    }

    public function test_config_defaults(): void
    {
        $response = $this->getJson($this->url('config'));

        $response->assertOk();
        $response->assertJsonPath('data.enabled', false);
        $response->assertJsonPath('data.rate_limit_max', 300);
        $response->assertJsonPath('data.rate_limit_window', 60);
        $response->assertJsonPath('data.ip_allowlist', ['*']);
    }

    public function test_save_config_enabled_and_rate_limit(): void
    {
        $response = $this->postJson($this->url('config'), [
            'enabled' => true,
            'rate_limit_max' => 120,
            'rate_limit_window' => 30,
        ]);

        $response->assertOk();
        $response->assertJsonPath('code', 0);

        $config = app(McpService::class)->config();
        $this->assertTrue($config['enabled']);
        $this->assertSame(120, $config['rate_limit_max']);
        $this->assertSame(30, $config['rate_limit_window']);
    }

    public function test_save_config_accepts_valid_allowlist(): void
    {
        $response = $this->postJson($this->url('config'), [
            'ip_allowlist' => ['192.168.1.10', '10.0.0.0/8', '172.16.0.1-172.16.0.50'],
        ]);

        $response->assertOk();
        $response->assertJsonPath('code', 0);
        $this->assertSame(
            ['192.168.1.10', '10.0.0.0/8', '172.16.0.1-172.16.0.50'],
            app(McpService::class)->config()['ip_allowlist']
        );
    }

    public function test_save_config_rejects_invalid_allowlist_entry(): void
    {
        $response = $this->postJson($this->url('config'), [
            'ip_allowlist' => ['not-an-ip'],
        ]);

        $response->assertOk();
        $response->assertJsonPath('code', 40001);
    }

    public function test_regenerate_prefix_changes_value(): void
    {
        $before = app(McpService::class)->config()['path_prefix'];

        $response = $this->postJson($this->url('config/regenerate-prefix'));

        $response->assertOk();
        $after = $response->json('data.path_prefix');
        $this->assertMatchesRegularExpression('#^/mcp-[A-Za-z0-9]{12}$#', $after);
        $this->assertNotSame($before, $after);
    }

    public function test_validate_allow_entry_rules(): void
    {
        $service = app(McpService::class);

        foreach (['*', '127.0.0.1', '10.0.0.0/8', '192.168.1.1-192.168.1.99'] as $valid) {
            $this->assertTrue($service->validateAllowEntry($valid), "应合法: {$valid}");
        }
        foreach (['', 'abc', '999.1.1.1', '192.168.1.9-192.168.1.1'] as $invalid) {
            $this->assertFalse($service->validateAllowEntry($invalid), "应非法: {$invalid}");
        }
    }

    // ============ 凭据管理 ============

    public function test_keys_initially_empty(): void
    {
        $response = $this->getJson($this->url('keys'));

        $response->assertOk();
        $response->assertJsonCount(0, 'data');
    }

    public function test_create_key_defaults_to_low_risk_only(): void
    {
        $response = $this->postJson($this->url('keys'), ['name' => '我的 Trae']);

        $response->assertOk();
        $response->assertJsonPath('code', 0);
        // 创建时返回一次明文令牌
        $this->assertStringStartsWith(McpKey::TOKEN_PREFIX, $response->json('data.token'));

        $key = McpKey::first();
        $this->assertNotNull($key);

        $lowIds = app(McpService::class)->lowRiskToolIds();
        $this->assertEqualsCanonicalizing($lowIds, $key->grantedTools());
        // 高风险工具应落在禁用集
        $this->assertContains('SystemExec', $key->disabledTools());
    }

    public function test_create_key_requires_name(): void
    {
        $response = $this->postJson($this->url('keys'), []);

        $response->assertStatus(422);
    }

    public function test_create_key_rejects_invalid_expires(): void
    {
        $response = $this->postJson($this->url('keys'), [
            'name' => 'x',
            'expires_days' => 123,
        ]);

        $response->assertStatus(422);
    }

    public function test_key_list_masks_token(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();
        $key = McpKey::first();

        $response = $this->getJson($this->url('keys'));

        $response->assertOk();
        $masked = $response->json('data.0.token_masked');
        $this->assertStringStartsWith('****', $masked);
        $this->assertStringNotContainsString((string) $key->token, $masked);
    }

    public function test_key_show_with_token_flag(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();
        $key = McpKey::first();

        // 默认不返回明文
        $plain = $this->getJson($this->url('keys/'.$key->id));
        $plain->assertOk();
        $this->assertArrayNotHasKey('token', $plain->json('data'));

        // 显式请求时返回明文（供复制）
        $withToken = $this->getJson($this->url('keys/'.$key->id.'?with_token=1'));
        $withToken->assertOk();
        $this->assertSame((string) $key->token, $withToken->json('data.token'));
    }

    public function test_update_key_tool_permissions(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();
        $key = McpKey::first();

        $response = $this->putJson($this->url('keys/'.$key->id), [
            'tool_ids' => ['SystemInfo', 'SystemExec'],
        ]);

        $response->assertOk();
        $key->refresh();

        $this->assertEqualsCanonicalizing(['SystemInfo', 'SystemExec'], $key->grantedTools());
        // 其余工具应进入禁用集，构成互斥分区
        $registered = app(McpService::class)->registeredToolIds();
        $this->assertCount(count($registered), array_merge($key->grantedTools(), $key->disabledTools()));
        $this->assertNotContains('SystemExec', $key->disabledTools());
    }

    public function test_update_key_ignores_unregistered_tools(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();
        $key = McpKey::first();

        $this->putJson($this->url('keys/'.$key->id), [
            'tool_ids' => ['SystemInfo', 'NoSuchTool'],
        ])->assertOk();

        $this->assertSame(['SystemInfo'], $key->fresh()->grantedTools());
    }

    public function test_update_key_name_and_expires(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();
        $key = McpKey::first();

        $this->putJson($this->url('keys/'.$key->id), [
            'name' => '新名字',
            'expires_days' => -1,
        ])->assertOk();

        $key->refresh();
        $this->assertSame('新名字', $key->name);
        $this->assertSame(-1, (int) $key->expires_days);
        $this->assertFalse($key->isExpired());
    }

    public function test_toggle_key(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();
        $key = McpKey::first();
        $this->assertTrue($key->isEffective());

        $this->postJson($this->url('keys/'.$key->id.'/toggle'))->assertOk();
        $this->assertFalse($key->fresh()->isEffective());

        $this->postJson($this->url('keys/'.$key->id.'/toggle'))->assertOk();
        $this->assertTrue($key->fresh()->isEffective());
    }

    public function test_delete_key(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();
        $key = McpKey::first();

        $this->deleteJson($this->url('keys/'.$key->id))->assertOk();
        $this->assertDatabaseMissing('app_cmspro_localservice_mcp_keys', ['id' => $key->id]);
    }

    public function test_low_risk_only_endpoint(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a', 'tool_ids' => ['SystemInfo', 'SystemExec']])->assertOk();
        $key = McpKey::first();

        $this->postJson($this->url('keys/'.$key->id.'/low-risk-only'))->assertOk();

        $lowIds = app(McpService::class)->lowRiskToolIds();
        $this->assertEqualsCanonicalizing($lowIds, $key->fresh()->grantedTools());
    }

    public function test_restore_default_endpoint(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a', 'tool_ids' => ['SystemInfo', 'SystemExec']])->assertOk();
        $key = McpKey::first();

        $this->postJson($this->url('keys/'.$key->id.'/restore-default'))->assertOk();

        $lowIds = app(McpService::class)->lowRiskToolIds();
        $this->assertEqualsCanonicalizing($lowIds, $key->fresh()->grantedTools());
    }

    public function test_update_missing_key_returns_404(): void
    {
        $this->putJson($this->url('keys/99999'), ['name' => 'x'])
            ->assertOk()
            ->assertJsonPath('code', 404);
    }

    // ============ 模型逻辑 ============

    public function test_key_effective_tools_excludes_disabled(): void
    {
        $key = new McpKey([
            'tool_ids' => json_encode(['SystemInfo', 'FileRead']),
            'disabled' => json_encode(['FileRead']),
        ]);

        $registered = ['SystemInfo', 'FileRead', 'SystemExec'];
        $this->assertSame(['SystemInfo'], $key->effectiveTools($registered));
    }

    public function test_key_partition_tools_forms_mutually_exclusive_sets(): void
    {
        $registered = ['A', 'B', 'C'];
        [$granted, $disabled] = McpKey::partitionTools(['A', 'X'], $registered);

        $this->assertSame(['A'], $granted);
        $this->assertSame(['B', 'C'], $disabled);
        $this->assertEmpty(array_intersect($granted, $disabled));
    }

    public function test_key_expiry_rules(): void
    {
        // create_time 由数据库写入（不在 fillable 中），故此处显式赋值后验证
        $never = new McpKey(['expires_days' => -1]);
        $never->create_time = now()->subYear();
        $this->assertFalse($never->isExpired());

        $fresh = new McpKey(['expires_days' => 7]);
        $fresh->create_time = now()->subDay();
        $this->assertFalse($fresh->isExpired());

        $stale = new McpKey(['expires_days' => 7]);
        $stale->create_time = now()->subDays(30);
        $this->assertTrue($stale->isExpired());

        // 创建时间缺失 → 按失效处理（安全兜底）
        $missing = new McpKey(['expires_days' => 7]);
        $this->assertTrue($missing->isExpired());
    }

    public function test_key_generates_unique_identifiers(): void
    {
        $ids = [];
        $tokens = [];
        for ($i = 0; $i < 5; $i++) {
            $ids[] = McpKey::generateKeyId();
            $tokens[] = McpKey::generateToken();
        }

        $this->assertCount(5, array_unique($ids));
        $this->assertCount(5, array_unique($tokens));
        $this->assertStringStartsWith(McpKey::KEY_ID_PREFIX, $ids[0]);
        $this->assertStringStartsWith(McpKey::TOKEN_PREFIX, $tokens[0]);
    }

    // ============ 配置下发与提示词 ============

    public function test_build_go_config_includes_credentials_and_access_control(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();
        $this->postJson($this->url('config'), ['enabled' => true])->assertOk();

        $config = app(McpService::class)->buildGoConfig();

        $this->assertTrue($config['enabled']);
        $this->assertMatchesRegularExpression('#^/mcp-[A-Za-z0-9]{12}$#', $config['path_prefix']);
        $this->assertNotEmpty($config['credentials']);
        $this->assertTrue($config['credentials'][0]['active']);
        $this->assertArrayHasKey('tool_ids', $config['credentials'][0]);
        $this->assertArrayHasKey('disabled', $config['credentials'][0]);
    }

    public function test_sync_config_writes_mcp_section_to_file(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'sync-test'])->assertOk();

        $response = $this->postJson($this->url('sync'));
        $response->assertOk();
        $response->assertJsonPath('code', 0);

        $path = storage_path('local_service_config.json');
        $this->assertFileExists($path);

        $config = json_decode((string) file_get_contents($path), true);
        $this->assertIsArray($config);
        $this->assertArrayHasKey('mcp', $config);
        $this->assertArrayHasKey('credentials', $config['mcp']);
        $this->assertNotEmpty($config['mcp']['credentials']);
    }

    public function test_prompt_contains_endpoint_and_token(): void
    {
        $this->postJson($this->url('keys'), ['name' => 'a'])->assertOk();

        $response = $this->getJson($this->url('prompt'));

        $response->assertOk();
        $prompt = (string) $response->json('data.prompt');
        $this->assertStringContainsString('mcpServers', $prompt);
        $this->assertStringContainsString('cmspro-local', $prompt);
        $this->assertStringContainsString('Authorization', $prompt);
        $this->assertStringContainsString('/mcp', $prompt);

        // 端点必须为本机地址（本地服务仅监听 127.0.0.1），不得使用站点域名
        $this->assertMatchesRegularExpression('#http://127\.0\.0\.1:\d+/mcp-#', $prompt);
        $this->assertStringNotContainsString((string) config('app.url', ''), $prompt);
    }

    public function test_config_requires_authentication(): void
    {
        // 未登录访问应被拦截
        $this->app['auth']->guard('admin')->logout();
        $this->getJson($this->url('config'))->assertStatus(401);
    }
}