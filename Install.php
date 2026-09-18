<?php

namespace App\Apps\CmsproLocalservice;

use App\Apps\CmsproLocalservice\Services\ScriptService;
use App\Apps\CmsproLocalservice\Services\ServiceManager;
use App\Apps\CmsproLocalservice\Models\CustomScript;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\Str;

class Install
{
    public function install(): void
    {
        $this->runMigrations();
        $this->ensureAuthToken();
        $this->initDefaultScripts();
        // 安装/重装后 token 可能已变更，同步刷新 Go 服务配置文件，
        // 避免运行中的 Go 进程持有旧 token 导致认证失败（服务显示离线）
        $this->syncGoConfig();
    }

    public function uninstall(): void
    {
        $this->rollbackMigrations();
        Cache::flush();
    }

    public function upgrade(string $fromVersion, string $toVersion): void
    {
        $this->runMigrations();
        $this->ensureAuthToken();
        $this->syncGoConfig();
    }

    /**
     * 将数据库中的配置同步写入 Go 服务配置文件。
     * 失败不阻塞安装流程（如 storage 目录不可写），仅记录日志。
     */
    protected function syncGoConfig(): void
    {
        try {
            app(ServiceManager::class)->syncConfig();
        } catch (\Throwable $e) {
            Log::warning('CmsproLocalservice: 同步 Go 服务配置文件失败，请到「基本配置」保存后重启服务', [
                'app' => 'cmspro.localservice',
                'error' => $e->getMessage(),
            ]);
        }
    }

    /**
     * 确保 auth_token 已生成。config_items 的 code 为 app_cmspro_localservice_auth_token（点号转下划线），
     * 此处用后缀 LIKE 匹配以兼容框架前缀拼接。
     */
    protected function ensureAuthToken(): void
    {
        $row = DB::table('config_items')
            ->where('code', 'like', '%localservice_auth_token')
            ->first();

        if ($row && !empty($row->value)) {
            return;
        }

        $token = Str::random(32);

        if ($row) {
            DB::table('config_items')->where('id', $row->id)->update(['value' => $token]);
        } else {
            Log::warning('CmsproLocalservice: auth_token 配置项不存在，跳过生成', ['app' => 'cmspro.localservice']);
        }
    }

    /**
     * 初始化 Linux 常用脚本库。
     * 应用安装时自动预置，不重复操作。
     */
    protected function initDefaultScripts(): void
    {
        if (CustomScript::where('platform', 'linux')->exists()) {
            Log::info('CmsproLocalservice: Linux 脚本库已存在，跳过初始化');
            return;
        }

        try {
            $service = app(ScriptService::class);
            $count = $service->initDefaultLinuxScripts();
            Log::info("CmsproLocalservice: 初始化 Linux 脚本库完成，共 {$count} 个脚本");
        } catch (\Throwable $e) {
            Log::warning('CmsproLocalservice: 初始化 Linux 脚本库失败，可后续手动操作', [
                'error' => $e->getMessage(),
            ]);
        }
    }

    protected function runMigrations(): void
    {
        $migrationsPath = __DIR__ . '/Migrations';
        if (!is_dir($migrationsPath)) {
            return;
        }
        $migrationFiles = glob($migrationsPath . '/*.php') ?: [];

        foreach ($migrationFiles as $file) {
            $migrationName = pathinfo($file, PATHINFO_FILENAME);

            if (DB::table('migrations')->where('migration', $migrationName)->exists()) {
                continue;
            }

            $migration = require $file;
            if (method_exists($migration, 'up')) {
                try {
                    $migration->up();
                    DB::table('migrations')->insert([
                        'migration' => $migrationName,
                        'batch' => DB::table('migrations')->max('batch') + 1,
                    ]);
                } catch (\Throwable $e) {
                    Log::error('应用迁移执行失败', [
                        'app' => 'cmspro.localservice',
                        'file' => basename($file),
                        'error' => $e->getMessage(),
                    ]);
                }
            }
        }
    }

    protected function rollbackMigrations(): void
    {
        $migrationsPath = __DIR__ . '/Migrations';
        if (!is_dir($migrationsPath)) {
            return;
        }
        $migrationFiles = glob($migrationsPath . '/*.php') ?: [];

        foreach (array_reverse($migrationFiles) as $file) {
            $migrationName = pathinfo($file, PATHINFO_FILENAME);

            if (!DB::table('migrations')->where('migration', $migrationName)->exists()) {
                continue;
            }

            $migration = require $file;
            if (method_exists($migration, 'down')) {
                try {
                    $migration->down();
                    DB::table('migrations')->where('migration', $migrationName)->delete();
                } catch (\Throwable $e) {
                    Log::error('应用迁移回滚失败', [
                        'app' => 'cmspro.localservice',
                        'file' => basename($file),
                        'error' => $e->getMessage(),
                    ]);
                }
            }
        }
    }
}
