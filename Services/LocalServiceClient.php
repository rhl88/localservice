<?php

namespace App\Apps\CmsproLocalservice\Services;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;

/**
 * 本地服务统一 HTTP 客户端。
 * 任意 CmsPro 应用通过 app(LocalServiceClient::class) 注入调用本地 Go 服务。
 */
class LocalServiceClient
{
    /** 本应用配置项缓存（code 去前缀后的 name => value），首次访问时惰性加载 */
    private ?array $configCache = null;

    /**
     * 同步执行任务，直接返回结果数组（Go 端 result.Data）。
     *
     * @param string $appId 调用方 app_id（审计用）
     * @param string $type  任务类型，如 file.read / browser.screenshot
     * @param array  $params 任务参数
     * @param int|null $timeout 单次请求超时（秒），null 用全局配置
     * @return array{code:int, message?:string, data:mixed}
     */
    public function exec(string $appId, string $type, array $params = [], ?int $timeout = null): array
    {
        return $this->postTask($appId, $type, 'sync', $params, $timeout);
    }

    /**
     * 异步执行任务，返回 task_id。失败时返回 null。
     */
    public function execAsync(string $appId, string $type, array $params = []): ?string
    {
        $res = $this->postTask($appId, $type, 'async', $params);
        if (($res['code'] ?? -1) === 0 && isset($res['data']['task_id'])) {
            return $res['data']['task_id'];
        }
        return null;
    }

    /**
     * 查询任务状态与结果。
     */
    public function getTaskStatus(string $taskId): array
    {
        return $this->request('GET', '/api/task/' . rawurlencode($taskId));
    }

    /**
     * 取消运行中的任务。
     */
    public function cancelTask(string $taskId): array
    {
        return $this->request('POST', '/api/task/' . rawurlencode($taskId) . '/cancel');
    }

    /**
     * 服务状态（版本、运行状态）。
     */
    public function status(): array
    {
        return $this->request('GET', '/api/status');
    }

    /**
     * 服务是否在线（HTTP 可达且 code=0）。
     *
     * @param int $timeout 单次探测超时（秒），0 表示使用全局配置。
     *                     重启后轮询确认等场景建议传短超时，避免探测阻塞拖长请求总时长。
     */
    public function isOnline(int $timeout = 0): bool
    {
        try {
            $res = $this->request('GET', '/api/status', [], $timeout > 0 ? $timeout : null);
            return ($res['code'] ?? -1) === 0;
        } catch (\Throwable $e) {
            return false;
        }
    }

    /**
     * 任务列表（分页）。
     */
    public function listTasks(array $query = []): array
    {
        return $this->request('GET', '/api/tasks', $query);
    }

    /**
     * 日志列表（分页）。
     */
    public function listLogs(array $query = []): array
    {
        return $this->request('GET', '/api/logs', $query);
    }

    /**
     * 清空全部日志（调试用）。
     */
    public function clearLogs(): array
    {
        return $this->request('POST', '/api/logs/clear');
    }

    /**
     * 清空全部任务记录（调试用）。
     */
    public function clearTasks(): array
    {
        return $this->request('POST', '/api/tasks/clear');
    }

    /**
     * 拓展列表。
     */
    public function listExtensions(): array
    {
        return $this->request('GET', '/api/extensions');
    }

    /**
     * 拓展详情。
     */
    public function getExtension(string $name): array
    {
        return $this->request('GET', '/api/extensions/' . rawurlencode($name));
    }

    /**
     * 更新拓展配置。
     *
     * @param array{path?:string, config?:string, status?:int} $data
     */
    public function updateExtension(string $name, array $data): array
    {
        return $this->request('PUT', '/api/extensions/' . rawurlencode($name), $data);
    }

    /**
     * 测试拓展可用性。
     */
    public function testExtension(string $name): array
    {
        return $this->request('POST', '/api/extensions/' . rawurlencode($name) . '/test');
    }

    /**
     * 提交任务（内部）。
     */
    protected function postTask(string $appId, string $type, string $mode, array $params, ?int $timeout = null): array
    {
        return $this->request('POST', '/api/task', [
            'app_id' => $appId,
            'type' => $type,
            'mode' => $mode,
            'params' => (object) $params,
        ], $timeout);
    }

    /**
     * 统一 HTTP 请求。GET 时 $payload 作为 query，其余作为 JSON body。
     * 网络异常统一转为 code=503 的错误结构，绝不抛出到调用方。
     * @param int|null $timeout 单次请求超时（秒），null 用全局配置
     */
    protected function request(string $method, string $path, array $payload = [], ?int $timeout = null): array
    {
        try {
            $http = Http::baseUrl($this->baseUrl())
                ->withToken($this->authToken())
                ->timeout($timeout ?? (int) $this->configItem('timeout', 30))
                ->acceptJson();

            if (strtoupper($method) === 'GET') {
                $response = $http->get($path, $payload);
            } elseif (strtoupper($method) === 'PUT') {
                $response = $http->put($path, $payload);
            } else {
                $response = $http->post($path, $payload);
            }

            $json = $response->json();
            if (!is_array($json)) {
                return ['code' => 500, 'message' => '本地服务返回非法响应', 'data' => null];
            }
            return $json;
        } catch (\Throwable $e) {
            return [
                'code' => 503,
                'message' => '本地服务不可用: ' . $e->getMessage(),
                'data' => null,
            ];
        }
    }

    /**
     * Go 服务基础 URL。
     */
    protected function baseUrl(): string
    {
        $port = $this->configItem('port', 9501);
        return 'http://127.0.0.1:' . $port;
    }

    /**
     * 认证 Token（从 config_items 读取）。
     */
    protected function authToken(): string
    {
        return (string) $this->configItem('auth_token', '');
    }

    /**
     * 读取应用配置项。config_items.code 为 app_cmspro_localservice_{name}（点号转下划线）。
     * 首次访问时一次性加载本应用全部配置到实例缓存，再按 name 精确取值——
     * 既避免 LIKE 下划线通配符的误匹配风险，也避免单次请求多次查库。
     * 未命中时回退到 Config/localservice.php 默认值。
     */
    protected function configItem(string $name, $default = null)
    {
        if ($this->configCache === null) {
            $this->configCache = [];
            $rows = DB::table('config_items')
                ->where('code', 'like', 'app_cmspro_localservice_%')
                ->pluck('value', 'code');
            foreach ($rows as $code => $value) {
                $key = substr($code, strlen('app_cmspro_localservice_'));
                $this->configCache[$key] = $value;
            }
        }

        $value = $this->configCache[$name] ?? null;
        if ($value === null || $value === '') {
            return config('apps.cmspro.localservice.' . $name, $default);
        }
        return $value;
    }
}
