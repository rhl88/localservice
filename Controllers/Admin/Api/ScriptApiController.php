<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin\Api;

use App\Apps\CmsproLocalservice\Services\ScriptService;
use App\Http\Responses\ApiResponse;
use Illuminate\Http\Request;

class ScriptApiController
{
    public function __construct(private ScriptService $service) {}

    /**
     * 分页列表。
     */
    public function index(Request $request)
    {
        $data = $this->service->paginate($request->only(['keyword', 'type', 'platform', 'status', 'page', 'page_size']));
        return ApiResponse::success($data);
    }

    /**
     * 全部启用脚本（下拉选择用）。
     */
    public function all()
    {
        return ApiResponse::success($this->service->allEnabled());
    }

    /**
     * 详情。
     */
    public function show(int $id)
    {
        $script = $this->service->find($id);
        if (!$script) {
            return ApiResponse::error(404, '脚本不存在');
        }
        return ApiResponse::success($script);
    }

    /**
     * 创建。
     */
    public function store(Request $request)
    {
        $data = $this->validate($request);
        if ($data instanceof \Illuminate\Http\JsonResponse) {
            return $data;
        }
        // 校验同平台内 name 唯一
        if (!$this->service->isNameUniqueInPlatform($data['name'], $data['platform'])) {
            return ApiResponse::error(422, "标识「{$data['name']}」在所选平台下已存在");
        }
        $script = $this->service->create($data);
        return ApiResponse::success($script, '创建成功');
    }

    /**
     * 更新。
     */
    public function update(Request $request, int $id)
    {
        $data = $this->validate($request, false);
        if ($data instanceof \Illuminate\Http\JsonResponse) {
            return $data;
        }
        // 若 name 或 platform 有变更，校验同平台唯一
        $name = $data['name'] ?? null;
        $platform = $data['platform'] ?? null;
        if ($name !== null || $platform !== null) {
            $existing = $this->service->find($id);
            if ($existing) {
                $checkName = $name ?? $existing->name;
                $checkPlatform = $platform ?? $existing->platform;
                if (!$this->service->isNameUniqueInPlatform($checkName, $checkPlatform, $id)) {
                    return ApiResponse::error(422, "标识「{$checkName}」在所选平台下已存在");
                }
            }
        }
        $script = $this->service->update($id, $data);
        if (!$script) {
            return ApiResponse::error(404, '脚本不存在');
        }
        return ApiResponse::success($script, '更新成功');
    }

    /**
     * 删除。
     */
    public function destroy(int $id)
    {
        if ($this->service->delete($id)) {
            return ApiResponse::success(null, '删除成功');
        }
        return ApiResponse::error(404, '脚本不存在');
    }

    /**
     * 初始化 Linux 常用脚本库。
     * 委托 ScriptService 完成批量插入。
     */
    public function initDefaults()
    {
        // 检查是否已初始化过（只要有 linux 平台的脚本就算已初始化）
        $exists = \App\Apps\CmsproLocalservice\Models\CustomScript::where('platform', 'linux')->exists();
        if ($exists) {
            return ApiResponse::error(409, 'Linux 脚本库已初始化，无需重复操作');
        }

        $inserted = $this->service->initDefaultLinuxScripts();
        return ApiResponse::success(['count' => $inserted], "成功初始化 {$inserted} 个 Linux 常用脚本");
    }

    public function execute(Request $request, int $id)
    {
        $args = $request->input('args', []);
        if (!is_array($args)) {
            $args = [];
        }
        $result = $this->service->execute($id, $args);
        if ($result['code'] !== 0) {
            return ApiResponse::error($result['code'], $result['message'], $result['data']);
        }
        return ApiResponse::success($result['data']);
    }

    /**
     * 参数校验。
     */
    private function validate(Request $request, bool $requireAll = true): array|\Illuminate\Http\JsonResponse
    {
        $rules = [
            'name' => ($requireAll ? 'required|' : 'sometimes|') . 'string|max:100|regex:/^[a-zA-Z0-9_\-]+$/',
            'title' => ($requireAll ? 'required|' : 'sometimes|') . 'string|max:200',
            'type' => ($requireAll ? 'required|' : 'sometimes|') . 'string|in:bat,ps1,sh,python',
            'platform' => ($requireAll ? 'required|' : 'sometimes|') . 'string|in:windows,linux,all',
            'content' => ($requireAll ? 'required|' : 'sometimes|') . 'string',
            'description' => 'nullable|string|max:1000',
            'params' => 'nullable|string|max:2000',
            'status' => 'sometimes|integer|in:0,1',
        ];

        $validator = validator($request->all(), $rules);
        if ($validator->fails()) {
            return response()->json([
                'code' => 422,
                'message' => '参数验证失败',
                'data' => $validator->errors()->toArray(),
                'timestamp' => time(),
            ], 422);
        }

        return $request->only(array_keys($rules));
    }
}
