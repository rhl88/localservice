<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Support\Facades\DB;

/**
 * 补建 MCP 服务配置项。
 *
 * 与既有 open_api_config_items 迁移同构：配置项需在安装时预建，
 * 否则运行时补建依赖配置分组存在（测试环境无分组会导致写入失败）。
 *
 * 配置项：启用开关、路由前缀、IP 白名单、限流阈值。
 * 注意：路由前缀留空，由 McpService 首次读取时生成不可预测值并写回。
 */
return new class extends Migration
{
    protected $connection = 'mysql';

    /** MCP 配置项：[code 后缀, 名称, 默认值, 类型] */
    private const ITEMS = [
        ['mcp_enabled', 'MCP服务开关', '0', 'switch'],
        ['mcp_path_prefix', 'MCP路由前缀', '', 'text'],
        ['mcp_ip_allowlist', 'MCP IP白名单', '*', 'text'],
        ['mcp_rate_limit_max', 'MCP限流上限', '300', 'number'],
        ['mcp_rate_limit_window', 'MCP限流窗口', '60', 'number'],
    ];

    public function up(): void
    {
        if (! DB::getSchemaBuilder()->hasTable('config_items')) {
            return;
        }

        // 归属「服务设置」配置组；组不存在时跳过（非标准安装环境）
        $groupId = DB::table('config_items')
            ->where('code', 'app_cmspro_localservice_port')
            ->value('group_id');

        if (! $groupId) {
            return;
        }

        foreach (self::ITEMS as $index => [$suffix, $name, $value, $type]) {
            $code = 'app_cmspro_localservice_'.$suffix;
            $exists = DB::table('config_items')->where('code', $code)->exists();
            if ($exists) {
                continue;
            }

            DB::table('config_items')->insert([
                'group_id' => $groupId,
                'name' => $name,
                'code' => $code,
                'value' => $value,
                'type' => $type,
                'sort' => 90 + $index,
                'status' => 1,
            ]);
        }
    }

    public function down(): void
    {
        foreach (self::ITEMS as [$suffix]) {
            DB::table('config_items')
                ->where('code', 'app_cmspro_localservice_'.$suffix)
                ->delete();
        }
    }
};