<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Support\Facades\DB;

/**
 * 新增开放 API 配置项：open_api_enabled、open_api_key。
 * 用于允许第三方跨服务器调用本地服务（仅暴露 /api/open/* 路由，需 X-API-Key 认证）。
 */
return new class extends Migration
{
    public function up(): void
    {
        $groupId = DB::table('config_items')
            ->where('code', 'app_cmspro_localservice_port')
            ->value('group_id');

        if (!$groupId) {
            return;
        }

        $exists = DB::table('config_items')
            ->where('code', 'app_cmspro_localservice_open_api_enabled')
            ->exists();
        if (!$exists) {
            DB::table('config_items')->insert([
                'group_id' => $groupId,
                'name' => '开放API开关',
                'code' => 'app_cmspro_localservice_open_api_enabled',
                'value' => '0',
                'type' => 'switch',
                'tips' => '开启后允许第三方通过 X-API-Key 调用本地服务',
                'sort' => 10,
                'status' => 1,
                'create_time' => now(),
                'update_time' => now(),
            ]);
        }

        $exists = DB::table('config_items')
            ->where('code', 'app_cmspro_localservice_open_api_key')
            ->exists();
        if (!$exists) {
            DB::table('config_items')->insert([
                'group_id' => $groupId,
                'name' => '开放API密钥',
                'code' => 'app_cmspro_localservice_open_api_key',
                'value' => '',
                'type' => 'text',
                'tips' => '第三方调用开放 API 时使用的密钥（X-API-Key 请求头）',
                'sort' => 11,
                'status' => 1,
                'create_time' => now(),
                'update_time' => now(),
            ]);
        }
    }

    public function down(): void
    {
        DB::table('config_items')
            ->whereIn('code', [
                'app_cmspro_localservice_open_api_enabled',
                'app_cmspro_localservice_open_api_key',
            ])
            ->delete();
    }
};
