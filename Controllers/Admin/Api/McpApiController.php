<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin\Api;

use App\Apps\CmsproLocalservice\Services\McpService;
use App\Http\Responses\ApiResponse;
use Illuminate\Http\Request;

/**
 * MCP 服务管理 API 控制器。
 *
 * 提供「接入与体验 / 安全授权」两类能力：
 * - 工具清单与分类统计（权限勾选界面数据源）
 * - 访问控制配置（开关、路由前缀、IP 白名单、限流）
 * - 凭据增删改查与权限矩阵维护
 * - 接入提示词生成与配置下发
 */
class McpApiController
{
    public function __construct(protected McpService $service)
    {
    }

    /**
     * 工具清单（按分类分组，含风险统计）
     */
    public function tools(): array
    {
        $result = $this->service->tools();

        // 按分类分组，供界面折叠展示
        $grouped = [];
        foreach ($result['tools'] as $tool) {
            $category = (string) ($tool['category'] ?? '其他');
            $grouped[$category][] = $tool;
        }

        return ApiResponse::success([
            'tools' => $result['tools'],
            'grouped' => $grouped,
            'total' => $result['total'],
            'risk_counts' => $result['risk_counts'],
            'source' => $result['source'],
        ]);
    }

    /**
     * 访问控制配置（含 MCP 端点地址与提示词）
     */
    public function config(): array
    {
        return ApiResponse::success($this->service->config() + [
            'prompt' => $this->service->buildPrompt(),
        ]);
    }

    /**
     * 保存访问控制配置
     */
    public function saveConfig(Request $request): array
    {
        $validated = $request->validate([
            'enabled' => 'sometimes|boolean',
            'ip_allowlist' => 'sometimes|array|max:50',
            'ip_allowlist.*' => 'string|max:64',
            'rate_limit_max' => 'sometimes|integer|min:1|max:100000',
            'rate_limit_window' => 'sometimes|integer|min:1|max:3600',
        ], [
            'ip_allowlist.array' => 'IP 白名单格式不正确',
            'rate_limit_max.min' => '限流上限至少为 1',
            'rate_limit_window.min' => '限流窗口至少为 1 秒',
        ]);

        // 白名单条目逐项校验（支持 *、IP、CIDR、A-B 区间）
        if (isset($validated['ip_allowlist'])) {
            foreach ($validated['ip_allowlist'] as $entry) {
                if (! $this->service->validateAllowEntry((string) $entry)) {
                    return ApiResponse::error(40001, 'IP 条目格式无效：'.$entry.'（支持 *、IP、CIDR 或 A-B 区间）');
                }
            }
        }

        $this->service->saveConfig($validated);
        $this->service->syncConfig();

        return ApiResponse::success(['saved' => true], '配置已保存，重新连接 MCP 服务后生效');
    }

    /**
     * 重新生成随机路由前缀（防端口扫描）
     */
    public function regeneratePathPrefix(): array
    {
        $prefix = $this->service->regeneratePathPrefix();
        $this->service->syncConfig();

        return ApiResponse::success(['path_prefix' => $prefix], '路由前缀已更新，请重新复制接入地址');
    }

    /**
     * 凭据列表
     */
    public function keys(): array
    {
        return ApiResponse::success($this->service->keys());
    }

    /**
     * 凭据详情（with_token=1 时返回明文令牌，供复制）
     */
    public function keyShow(Request $request, int $id): array
    {
        $detail = $this->service->keyDetail($id, $request->boolean('with_token'));
        if ($detail === null) {
            return ApiResponse::error(404, '凭据不存在');
        }

        return ApiResponse::success($detail);
    }

    /**
     * 创建凭据（未指定工具时默认仅授权低风险工具）
     */
    public function keyStore(Request $request): array
    {
        $validated = $request->validate([
            'name' => 'required|string|max:60',
            'tool_ids' => 'sometimes|array|max:200',
            'tool_ids.*' => 'string|max:60',
            'expires_days' => 'sometimes|integer|in:-1,7,30,90',
        ], [
            'name.required' => '请填写凭据名称',
        ]);

        $key = $this->service->createKey($validated);

        // 创建后返回一次明文令牌（列表接口只返回掩码）
        return ApiResponse::success([
            'id' => $key->id,
            'token' => (string) $key->token,
            'key_id' => (string) $key->key_id,
        ], '凭据已创建，请立即复制令牌（此后不再完整显示）');
    }

    /**
     * 更新凭据（名称 / 有效期 / 启停 / 授权工具集）
     */
    public function keyUpdate(Request $request, int $id): array
    {
        $validated = $request->validate([
            'name' => 'sometimes|string|max:60',
            'tool_ids' => 'sometimes|array|max:200',
            'tool_ids.*' => 'string|max:60',
            'expires_days' => 'sometimes|integer|in:-1,7,30,90',
            'active' => 'sometimes|boolean',
        ]);

        $key = $this->service->updateKey($id, $validated);
        if ($key === null) {
            return ApiResponse::error(404, '凭据不存在');
        }

        return ApiResponse::success(['updated' => true], '凭据已更新');
    }

    /**
     * 删除凭据
     */
    public function keyDestroy(int $id): array
    {
        if (! $this->service->deleteKey($id)) {
            return ApiResponse::error(404, '凭据不存在');
        }

        return ApiResponse::success(null, '凭据已删除');
    }

    /**
     * 切换凭据启停
     */
    public function keyToggle(int $id): array
    {
        $detail = $this->service->keyDetail($id);
        if ($detail === null) {
            return ApiResponse::error(404, '凭据不存在');
        }

        $key = $this->service->updateKey($id, ['active' => (int) $detail['active'] !== 1]);

        return ApiResponse::success(
            ['active' => (int) $key->active],
            (int) $key->active === 1 ? '凭据已启用' : '凭据已停用'
        );
    }

    /**
     * 仅授权低风险工具
     */
    public function keyLowRiskOnly(int $id): array
    {
        $key = $this->service->grantLowRiskOnly($id);
        if ($key === null) {
            return ApiResponse::error(404, '凭据不存在');
        }

        return ApiResponse::success(null, '已重置为仅授权低风险工具');
    }

    /**
     * 恢复默认授权（等价于仅授权低风险）
     */
    public function keyRestoreDefault(int $id): array
    {
        $key = $this->service->restoreDefault($id);
        if ($key === null) {
            return ApiResponse::error(404, '凭据不存在');
        }

        return ApiResponse::success(null, '已恢复默认授权');
    }

    /**
     * 获取接入提示词
     */
    public function prompt(): array
    {
        return ApiResponse::success(['prompt' => $this->service->buildPrompt()]);
    }

    /**
     * 手动触发配置下发（凭据或访问控制变更后调用）
     */
    public function sync(): array
    {
        $path = $this->service->syncConfig();

        return ApiResponse::success(['synced' => true, 'config_path' => $path], '配置已下发，重启本地服务后生效');
    }
}