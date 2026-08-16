<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin\Api;

use App\Apps\CmsproLocalservice\Services\LocalServiceClient;
use App\Http\Responses\ApiResponse;
use Illuminate\Http\Request;

class TaskApiController
{
    public function __construct(private LocalServiceClient $client) {}

    /**
     * 任务列表（分页 + 筛选）。
     */
    public function index(Request $request)
    {
        $res = $this->client->listTasks($request->only(['app_id', 'status', 'page', 'page_size']));
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '获取任务失败');
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 任务详情。
     */
    public function show($id)
    {
        $res = $this->client->getTaskStatus($id);
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '获取任务详情失败');
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 取消任务。
     */
    public function cancel($id)
    {
        $res = $this->client->cancelTask($id);
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '取消任务失败');
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 清空全部任务记录（调试用）。
     */
    public function clear()
    {
        $res = $this->client->clearTasks();
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '清空任务记录失败');
        }
        return ApiResponse::success($res['data']);
    }
}
