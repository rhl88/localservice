<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin\Api;

use App\Http\Responses\ApiResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Str;

class SettingApiController
{
    /** 可编辑的配置项白名单 */
    private const ITEMS = ['port', 'auth_token', 'log_level', 'log_retention_days', 'open_api_enabled', 'open_api_key', 'timezone'];

    /**
     * 读取当前设置。
     */
    public function index()
    {
        $data = [];
        foreach (self::ITEMS as $name) {
            $data[$name] = $this->read($name);
        }
        return ApiResponse::success($data);
    }

    /**
     * 保存设置。auth_token 不可通过 save 修改（请用 regenerateToken）。
     */
    public function save(Request $request)
    {
        $payload = $request->validate([
            'port' => 'required|integer|min:1|max:65535',
            'log_level' => 'required|in:debug,info,warn,error',
            'log_retention_days' => 'required|integer|min:1|max:3650',
            'open_api_enabled' => 'sometimes|integer|in:0,1',
            'open_api_key' => 'sometimes|nullable|string|max:64',
            'timezone' => 'sometimes|string|max:64',
        ]);

        foreach ($payload as $name => $value) {
            $this->write($name, (string) $value);
        }

        return ApiResponse::success(['saved' => true]);
    }

    /**
     * 重新生成认证 Token。
     */
    public function regenerateToken()
    {
        $token = Str::random(32);
        $this->write('auth_token', $token);
        return ApiResponse::success(['auth_token' => $token]);
    }

    /**
     * 重新生成开放 API 密钥。
     */
    public function regenerateOpenApiKey()
    {
        $key = Str::random(32);
        $this->write('open_api_key', $key);
        return ApiResponse::success(['open_api_key' => $key]);
    }

    /**
     * 读取配置项（后缀 LIKE 匹配 code：app_cmspro_localservice_{name}）。
     */
    private function read(string $name): ?string
    {
        return DB::table('config_items')
            ->where('code', 'like', '%localservice_' . $name)
            ->value('value');
    }

    /**
     * 写入配置项。
     */
    private function write(string $name, string $value): void
    {
        DB::table('config_items')
            ->where('code', 'like', '%localservice_' . $name)
            ->update(['value' => $value]);
    }
}
