<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin\Api;

use App\Apps\CmsproLocalservice\Services\LocalServiceClient;
use App\Http\Responses\ApiResponse;
use Illuminate\Http\Request;

class LogApiController
{
    public function __construct(private LocalServiceClient $client) {}

    /**
     * 日志列表（分页 + 筛选）。
     */
    public function index(Request $request)
    {
        $res = $this->client->listLogs($request->only(['app_id', 'level', 'page', 'page_size']));
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '获取日志失败');
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 清空全部日志（调试用）。
     */
    public function clear()
    {
        $res = $this->client->clearLogs();
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '清空日志失败');
        }
        return ApiResponse::success($res['data']);
    }
}
