<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Schema;

/**
 * 指令表增加 platform 字段，唯一索引从 name 改为 name+platform。
 *
 * 同一平台内 name 唯一，不同平台可有相同 name（如 install-python 在 windows 和 linux 各一条）。
 */
return new class extends Migration
{
    protected $connection = 'mysql';

    public function up(): void
    {
        // 1. 添加 platform 字段（先给默认值 all，再按 type 回填）
        Schema::table('app_cmspro_localservice_custom_scripts', function (Blueprint $table) {
            $table->string('platform', 20)->default('all')->comment('适用平台: windows/linux/all')->after('type');
        });

        // 2. 按 type 回填现有数据的 platform
        // bat/ps1 → windows，sh → linux，python → all
        DB::table('app_cmspro_localservice_custom_scripts')
            ->whereIn('type', ['bat', 'ps1'])
            ->update(['platform' => 'windows']);
        DB::table('app_cmspro_localservice_custom_scripts')
            ->where('type', 'sh')
            ->update(['platform' => 'linux']);
        DB::table('app_cmspro_localservice_custom_scripts')
            ->where('type', 'python')
            ->update(['platform' => 'all']);

        // 3. 删除旧唯一索引，建新联合唯一索引
        Schema::table('app_cmspro_localservice_custom_scripts', function (Blueprint $table) {
            $table->dropUnique('idx_scr_name');
            $table->unique(['name', 'platform'], 'idx_scr_name_platform');
            $table->index('platform', 'idx_scr_platform');
        });
    }

    public function down(): void
    {
        Schema::table('app_cmspro_localservice_custom_scripts', function (Blueprint $table) {
            $table->dropIndex('idx_scr_platform');
            $table->dropUnique('idx_scr_name_platform');
            $table->unique('name', 'idx_scr_name');
        });

        Schema::table('app_cmspro_localservice_custom_scripts', function (Blueprint $table) {
            $table->dropColumn('platform');
        });
    }
};
