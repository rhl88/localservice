<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

/**
 * MCP 服务访问凭据表。
 *
 * 每个凭据独立维护工具授权集（tool_ids）与显式禁用集（disabled），
 * 两者对「当前已注册工具」构成互斥分区；低风险工具默认授权，
 * 中/高风险默认不授权（opt-in）。凭据由后台管理，经 syncConfig 下发给 Go 服务。
 */
return new class extends Migration
{
    protected $connection = 'mysql';

    public function up(): void
    {
        if (Schema::hasTable('app_cmspro_localservice_mcp_keys')) {
            return;
        }

        Schema::create('app_cmspro_localservice_mcp_keys', function (Blueprint $table) {
            $table->id();
            $table->string('key_id', 40)->comment('凭据标识，如 key_a1b2c3');
            $table->string('name', 60)->comment('凭据名称（展示用）');
            $table->string('token', 120)->comment('访问令牌（Bearer）');
            $table->text('tool_ids')->nullable()->comment('已授权工具标识（JSON 数组）');
            $table->text('disabled')->nullable()->comment('显式禁用工具标识（JSON 数组）');
            $table->integer('expires_days')->default(7)->comment('有效期天数：-1=永不过期，N=创建后 N 天失效');
            $table->unsignedTinyInteger('active')->default(1)->comment('启停状态：1=启用 0=停用');
            $table->timestamp('create_time')->nullable()->comment('创建时间');
            $table->timestamp('update_time')->nullable()->comment('更新时间');

            $table->unique('key_id', 'idx_mcp_key_id');
            $table->unique('token', 'idx_mcp_key_token');
            $table->index('active', 'idx_mcp_key_active');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('app_cmspro_localservice_mcp_keys');
    }
};