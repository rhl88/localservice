<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin\Api;

use App\Apps\CmsproLocalservice\Services\LocalServiceClient;
use App\Apps\CmsproLocalservice\Services\ServiceManager;
use App\Apps\CmsproLocalservice\Services\UpdateService;
use App\Http\Responses\ApiResponse;

class ServiceApiController
{
    public function __construct(
        private ServiceManager $manager,
        private LocalServiceClient $client,
        private UpdateService $updater
    ) {}

    /**
     * 服务状态 + 累计统计。
     * 累计统计通过 Go 端 listTasks 获取（支持 app_id/status 分页，无日期过滤）。
     */
    public function status()
    {
        $status = $this->manager->status();

        $total = 0;
        $success = 0;
        if ($status['online']) {
            $res = $this->client->listTasks(['page' => 1, 'page_size' => 1]);
            $total = $res['data']['total'] ?? 0;

            $okRes = $this->client->listTasks(['status' => 'success', 'page' => 1, 'page_size' => 1]);
            $success = $okRes['data']['total'] ?? 0;
        }

        return ApiResponse::success([
            'service' => $status,
            'stats' => [
                'total' => $total,
                'success' => $success,
                'success_rate' => $total > 0 ? round($success / $total * 100, 1) : 0,
            ],
        ]);
    }

    /**
     * 生成安装脚本。
     */
    public function installScript()
    {
        $res = $this->manager->installScript();
        if ($res['code'] !== 0) {
            return ApiResponse::error($res['code'], $res['message']);
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 生成卸载脚本。
     */
    public function uninstallScript()
    {
        $res = $this->manager->uninstallScript();
        if ($res['code'] !== 0) {
            return ApiResponse::error($res['code'], $res['message']);
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 启动服务。
     */
    public function start()
    {
        $res = $this->manager->start();
        if ($res['code'] !== 0) {
            return ApiResponse::error($res['code'], $res['message']);
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 停止服务。
     */
    public function stop()
    {
        $res = $this->manager->stop();
        if ($res['code'] !== 0) {
            return ApiResponse::error($res['code'], $res['message']);
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 重启服务。
     */
    public function restart()
    {
        $res = $this->manager->restart();
        if ($res['code'] !== 0) {
            return ApiResponse::error($res['code'], $res['message']);
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 检查本地服务更新（对比版本服务器最新已发布版本）。
     */
    public function updateCheck()
    {
        $res = $this->updater->checkUpdate();
        if ($res['code'] !== 0) {
            return ApiResponse::error($res['code'], $res['message']);
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 发起本地服务更新：校验环境并创建更新任务，返回 run_id 供前端分步驱动。
     */
    public function updateStart()
    {
        $res = $this->updater->startUpdate();
        if ($res['code'] !== 0) {
            return ApiResponse::error($res['code'], $res['message'], $res['data']);
        }
        return ApiResponse::success($res['data'], $res['message'] ?? null);
    }

    /**
     * 执行更新任务的下一步（download/replace/verify 单步），前端循环调用直至终态。
     */
    public function updateStep(\Illuminate\Http\Request $request)
    {
        $res = $this->updater->stepUpdate((string) $request->input('run_id', ''));
        if ($res['code'] !== 0) {
            return ApiResponse::error($res['code'], $res['message'], $res['data']);
        }
        return ApiResponse::success($res['data'], $res['message'] ?? null);
    }

    /**
     * 查询更新任务状态（无任务时 data 为 null，供页面刷新后恢复轮询）。
     */
    public function updateStatus()
    {
        $res = $this->updater->updateStatus();
        return ApiResponse::success($res['data']);
    }

    /**
     * 已安装应用列表（app_id → name 映射，供前端显示用）。
     */
    public function installedApps()
    {
        $apps = [];
        $appsDir = base_path('app/Apps');
        if (!is_dir($appsDir)) {
            return ApiResponse::success($apps);
        }

        $dirs = scandir($appsDir);
        foreach ($dirs as $dir) {
            if ($dir === '.' || $dir === '..') {
                continue;
            }
            $manifestPath = $appsDir . '/' . $dir . '/manifest.json';
            if (!file_exists($manifestPath)) {
                continue;
            }
            $manifest = json_decode(file_get_contents($manifestPath), true);
            if (!$manifest) {
                continue;
            }
            $appId = $manifest['id'] ?? '';
            $name = $manifest['name'] ?? $dir;
            if ($appId) {
                $apps[] = ['app_id' => $appId, 'name' => $name];
            }
        }

        return ApiResponse::success($apps);
    }
}
