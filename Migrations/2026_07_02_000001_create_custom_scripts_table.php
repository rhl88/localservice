<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    protected $connection = 'mysql';

    public function up(): void
    {
        Schema::create('app_cmspro_localservice_custom_scripts', function (Blueprint $table) {
            $table->id();
            $table->string('name', 100)->comment('脚本标识，如 my_backup');
            $table->string('title', 200)->comment('脚本名称，如 数据库备份');
            $table->string('type', 20)->default('bat')->comment('脚本类型: bat/ps1/sh/python');
            $table->text('content')->comment('脚本内容');
            $table->text('description')->nullable()->comment('脚本说明');
            $table->text('params')->nullable()->comment('参数说明，JSON 格式');
            $table->unsignedTinyInteger('status')->default(1)->comment('状态: 1=启用 0=禁用');
            $table->timestamps();

            $table->unique('name', 'idx_scr_name');
            $table->index('status', 'idx_scr_status');
            $table->index('type', 'idx_scr_type');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('app_cmspro_localservice_custom_scripts');
    }
};
