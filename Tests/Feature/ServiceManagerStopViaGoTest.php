<?php

namespace App\Apps\CmsproLocalservice\Tests\Feature;

use App\Apps\CmsproLocalservice\Services\ServiceManager;
use App\Apps\CmsproLocalservice\Tests\LocalserviceTestCase;
use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Http;

/**
 * 覆写平台判定的测试替身：测试机为 Windows，强制走 Linux 分支逻辑
 */
class LinuxTestServiceManager extends ServiceManager
{
    protected function isWindows(): bool
    {
        return false;
    }

    /**
     * 模拟 PHP exec/shell_exec 均不可用（disable_functions），触发经 Go 降级
     */
    protected function safeShellExec(string $cmd): ?array
    {
        return null;
    }
}

/**
 * 覆写下线判定的测试替身：仅用于验证「响应中断 + 下线确认失败」的报错分支
 */
class OfflineConfirmFailManager extends LinuxTestServiceManager
{
    protected function waitServiceOffline(): bool
    {
        return false;
    }
}

/**
 * ServiceManager · 经 Go 降级停止服务的响应中断判定测试
 *
 * 背景：Linux 下更新流程经 Go 服务执行 systemctl stop 停止 Go 自身，
 * 进程被 SIGTERM 终止时 HTTP 响应必然中断（cURL error 52），属预期现象，
 * 应以「服务已下线」判定停止成功，而非误报失败中止更新。
 */
class ServiceManagerStopViaGoTest extends LocalserviceTestCase
{
    protected function tearDown(): void
    {
        \Mockery::close();
        parent::tearDown();
    }

    /**
     * 按请求次序模拟本地 Go 服务的行为：
     * status 首次在线；task 首次（布置延迟启动）由 $launchBehavior 决定（默认成功），
     * task 第二次（停止命令）由 $stopBehavior 决定；
     * 后续 status 探测由 $offlineBehavior 决定（抛连接异常=下线，正常响应=仍在线）。
     * $capturedCommands 引用收集每次 task 请求的 command 参数，供断言布置方式。
     */
    protected function fakeGoService(callable $stopBehavior, callable $offlineBehavior, ?array &$capturedCommands = null, ?callable $launchBehavior = null): void
    {
        $statusCount = 0;
        $taskCount = 0;
        Http::fake(function ($request) use (&$statusCount, &$taskCount, &$capturedCommands, $stopBehavior, $offlineBehavior, $launchBehavior) {
            $url = $request->url();
            if (str_contains($url, '/api/status')) {
                $statusCount++;
                if ($statusCount === 1) {
                    return Http::response(['code' => 0, 'message' => 'success', 'data' => ['version' => '1.0.0']]);
                }
                return $offlineBehavior();
            }
            if (str_contains($url, '/api/task')) {
                $taskCount++;
                $payload = $request->data();
                $params = (array) ($payload['params'] ?? []);
                $capturedCommands[] = (string) ($params['command'] ?? '');
                if ($taskCount === 1) {
                    // 第一次 task：布置延迟启动，行为由 $launchBehavior 决定
                    return $launchBehavior !== null
                        ? $launchBehavior()
                        : Http::response(['code' => 0, 'message' => 'success', 'data' => ['stdout' => '', 'exit_code' => 0]]);
                }
                // 第二次 task：执行 systemctl stop，行为由场景决定
                return $stopBehavior();
            }
            return Http::response(['code' => 0, 'message' => 'success', 'data' => null]);
        });
    }

    /**
     * 模拟连接层异常（cURL error 52 等）：服务进程随停止命令退出，HTTP 响应中断
     */
    protected function curlError(): mixed
    {
        throw new ConnectionException('cURL error 52: Empty reply from server for http://127.0.0.1:9501/api/task');
    }

    public function test_停止响应中断但服务已下线时判定停止成功(): void
    {
        // 停止命令响应中断（cURL 52）；后续 status 探测连接拒绝（服务已下线）
        $captured = [];
        $this->fakeGoService(
            fn () => $this->curlError(),
            fn () => $this->curlError(),
            $captured
        );
        $manager = new LinuxTestServiceManager();

        $res = $manager->stop(true);

        $this->assertEquals(0, $res['code'], $res['message'] ?? '');
        $this->assertTrue($res['data']['delayed_start']);
        // 延迟启动经 systemd-run 一次性 timer 布置（由 PID 1 管理，不受服务 cgroup 清理影响），
        // 精度属性须用 --timer-property=AccuracySec=1s（systemd-run 无 --accuracy 选项）
        $this->assertStringContainsString('systemd-run', $captured[0]);
        $this->assertStringContainsString('--timer-property=AccuracySec=1s', $captured[0]);
        $this->assertStringContainsString('systemctl start', $captured[0]);
    }

    public function test_布置延迟启动命令失败时错误信息携带退出码与原始输出(): void
    {
        // 布置命令 HTTP 响应正常（code=0）但执行失败（如 systemd-run 参数错误）：
        // 错误信息必须给出退出码与 stdout，不能被 message（success）掩盖
        $this->fakeGoService(
            fn () => $this->curlError(),
            fn () => $this->curlError(),
            $captured,
            fn () => Http::response(['code' => 0, 'message' => 'success', 'data' => ['stdout' => 'systemd-run: unrecognized option', 'exit_code' => 125]])
        );
        $manager = new LinuxTestServiceManager();

        $res = $manager->stop(true);

        $this->assertEquals(500, $res['code']);
        $this->assertStringContainsString('布置延迟启动进程失败', $res['message']);
        $this->assertStringContainsString('退出码 125', $res['message']);
        $this->assertStringContainsString('unrecognized option', $res['message']);
    }

    public function test_停止响应中断且服务未下线时返回失败(): void
    {
        // 停止命令响应中断，但下线确认失败（覆写 waitServiceOffline 返回 false 消除真实轮询等待）
        $this->fakeGoService(
            fn () => $this->curlError(),
            fn () => Http::response(['code' => 0, 'message' => 'success', 'data' => ['version' => '1.0.0']])
        );
        $manager = new OfflineConfirmFailManager();

        $res = $manager->stop(true);

        $this->assertEquals(500, $res['code']);
        $this->assertStringContainsString('经本地服务执行停止命令失败', $res['message']);
    }

    public function test_停止响应正常但退出码非0时返回失败(): void
    {
        // systemctl 明确报错（如 unit 不存在）：响应正常返回、退出码非 0，应提前失败
        $this->fakeGoService(
            fn () => Http::response(['code' => 0, 'message' => 'success', 'data' => ['stdout' => 'Failed to stop unit', 'exit_code' => 5]]),
            fn () => $this->curlError()
        );
        $manager = new LinuxTestServiceManager();

        $res = $manager->stop(true);

        $this->assertEquals(500, $res['code']);
        $this->assertStringContainsString('退出码 5', $res['message']);
        $this->assertStringContainsString('Failed to stop unit', $res['message']);
    }

    public function test_停止响应正常且退出码为0时成功(): void
    {
        $this->fakeGoService(
            fn () => Http::response(['code' => 0, 'message' => 'success', 'data' => ['stdout' => '', 'exit_code' => 0]]),
            fn () => $this->curlError()
        );
        $manager = new LinuxTestServiceManager();

        $res = $manager->stop(true);

        $this->assertEquals(0, $res['code'], $res['message'] ?? '');
        $this->assertTrue($res['data']['delayed_start']);
    }
}
