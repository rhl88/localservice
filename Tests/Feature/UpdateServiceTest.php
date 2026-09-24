<?php

namespace App\Apps\CmsproLocalservice\Tests\Feature;

use App\Apps\CmsproLocalservice\Services\ServiceManager;
use App\Apps\CmsproLocalservice\Services\UpdateService;
use App\Apps\CmsproLocalservice\Tests\LocalserviceTestCase;
use Illuminate\Support\Facades\File;
use Illuminate\Support\Facades\Http;

/**
 * 覆写等待逻辑的测试替身：消除 sleep 等待，使更新流程即时完成
 */
class InstantUpdateService extends UpdateService
{
    protected function waitProcessExit(): void {}

    protected function probeDelay(): void {}
}

class UpdateServiceTest extends LocalserviceTestCase
{
    /** 测试用假二进制文件路径 */
    protected string $fakeBinary = '';

    protected function tearDown(): void
    {
        if ($this->fakeBinary !== '') {
            @unlink($this->fakeBinary);
            @unlink($this->fakeBinary . '.bak');
        }
        $updateDir = storage_path('app/localservice/update');
        if (is_dir($updateDir)) {
            File::deleteDirectory($updateDir);
        }
        \Mockery::close();
        parent::tearDown();
    }

    /**
     * 创建一个内容为 OLD-BINARY 的假二进制文件
     */
    protected function fakeBinary(): string
    {
        $this->fakeBinary = storage_path('app/localservice/update-test/fake-service-' . uniqid() . '.bin');
        if (!is_dir(dirname($this->fakeBinary))) {
            @mkdir(dirname($this->fakeBinary), 0755, true);
        }
        file_put_contents($this->fakeBinary, 'OLD-BINARY-CONTENT');
        return $this->fakeBinary;
    }

    /**
     * 构造被 mock 的 ServiceManager
     */
    protected function mockManager(string $binaryPath, array $stopResult = ['code' => 0, 'message' => 'success'], array $startResult = ['code' => 0, 'message' => 'success']): ServiceManager
    {
        $manager = \Mockery::mock(ServiceManager::class)->makePartial()->shouldAllowMockingProtectedMethods();
        $manager->shouldReceive('currentBinaryPath')->andReturn($binaryPath);
        $manager->shouldReceive('stop')->andReturn($stopResult)->byDefault();
        $manager->shouldReceive('start')->andReturn($startResult)->byDefault();
        $this->app->instance(ServiceManager::class, $manager);
        return $manager;
    }

    /**
     * 注册 HTTP fake：本地 Go 服务状态 + 版本服务器 latest/download。
     * status 支持传入 ResponseSequence（按请求次序返回不同响应，用于验证分阶段场景）。
     */
    protected function fakeHttp(array $overrides = []): void
    {
        $status = $overrides['status'] ?? ['code' => 0, 'data' => ['version' => '1.0.0', 'status' => 'running']];
        $latest = $overrides['latest'] ?? [
            'code' => 0,
            'data' => [
                'version' => '1.1.0',
                'changelog' => '新版修复',
                'release_date' => '2026-09-24 12:00:00',
                'file_size' => strlen('NEW-BINARY-CONTENT'),
                'checksum' => hash('sha256', 'NEW-BINARY-CONTENT'),
                'download_url' => '/api/versionsvr/local-service/download/1.1.0?platform=windows',
            ],
        ];
        $download = $overrides['download'] ?? 'NEW-BINARY-CONTENT';

        Http::fake([
            '127.0.0.1:9501/api/status' => $status instanceof \Illuminate\Http\Client\ResponseSequence ? $status : Http::response($status),
            'v5.cmspro.cn/api/versionsvr/local-service/latest*' => Http::response($latest),
            'v5.cmspro.cn/api/versionsvr/local-service/download*' => Http::response($download),
        ]);
    }

    protected function makeService(): InstantUpdateService
    {
        return new InstantUpdateService(app(ServiceManager::class), app(\App\Apps\CmsproLocalservice\Services\LocalServiceClient::class));
    }

    /**
     * 更新状态文件路径（与 UpdateService::statePath 约定一致）
     */
    protected function statePath(): string
    {
        return storage_path('app/localservice/update/update_state.json');
    }

    /**
     * 完整走完 start + download，返回服务实例与 run_id
     */
    protected function startAndDownload(InstantUpdateService $service): string
    {
        $res = $service->startUpdate();
        $this->assertEquals(0, $res['code'], $res['message'] ?? '');
        $runId = $res['data']['run_id'];

        $stepRes = $service->stepUpdate($runId);
        $this->assertEquals('replace', $stepRes['data']['step'] ?? null, 'download 步骤应完成并转入 replace');
        return $runId;
    }

    // ============ 检查更新 ============

    public function test_当前平台标识为windows(): void
    {
        $service = new UpdateService(app(ServiceManager::class), app(\App\Apps\CmsproLocalservice\Services\LocalServiceClient::class));
        $this->assertEquals('windows', $service->platform());
    }

    public function test_服务离线时检查更新返回503(): void
    {
        $this->fakeHttp(['status' => ['code' => 503, 'message' => '本地服务不可用']]);
        $service = new UpdateService(app(ServiceManager::class), app(\App\Apps\CmsproLocalservice\Services\LocalServiceClient::class));

        $res = $service->checkUpdate();

        $this->assertEquals(503, $res['code']);
    }

    public function test_远程无已发布版本时返回50006(): void
    {
        $this->fakeHttp(['latest' => ['code' => 50006, 'message' => '暂无已发布的本地服务版本']]);
        $service = new UpdateService(app(ServiceManager::class), app(\App\Apps\CmsproLocalservice\Services\LocalServiceClient::class));

        $res = $service->checkUpdate();

        $this->assertEquals(50006, $res['code']);
    }

    public function test_远程有新版本时has_update为true(): void
    {
        $this->fakeHttp();
        $service = new UpdateService(app(ServiceManager::class), app(\App\Apps\CmsproLocalservice\Services\LocalServiceClient::class));

        $res = $service->checkUpdate();

        $this->assertEquals(0, $res['code']);
        $this->assertTrue($res['data']['has_update']);
        $this->assertEquals('1.0.0', $res['data']['current_version']);
        $this->assertEquals('1.1.0', $res['data']['latest_version']);
    }

    public function test_版本一致时has_update为false(): void
    {
        $this->fakeHttp(['latest' => [
            'code' => 0,
            'data' => [
                'version' => '1.0.0',
                'changelog' => '',
                'release_date' => '2026-09-24 12:00:00',
                'file_size' => 1,
                'checksum' => 'x',
                'download_url' => '/api/versionsvr/local-service/download/1.0.0?platform=windows',
            ],
        ]]);
        $service = new UpdateService(app(ServiceManager::class), app(\App\Apps\CmsproLocalservice\Services\LocalServiceClient::class));

        $res = $service->checkUpdate();

        $this->assertEquals(0, $res['code']);
        $this->assertFalse($res['data']['has_update']);
    }

    // ============ start 步骤 ============

    public function test_已是最新版本时startUpdate返回50010(): void
    {
        $this->fakeHttp(['latest' => [
            'code' => 0,
            'data' => [
                'version' => '1.0.0',
                'changelog' => '',
                'release_date' => '2026-09-24 12:00:00',
                'file_size' => 1,
                'checksum' => 'x',
                'download_url' => '/api/versionsvr/local-service/download/1.0.0?platform=windows',
            ],
        ]]);
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $res = $service->startUpdate();

        $this->assertEquals(50010, $res['code']);
        // 未修改任何文件、未创建状态文件
        $this->assertEquals('OLD-BINARY-CONTENT', file_get_contents($binary));
        $this->assertFileDoesNotExist($this->statePath());
    }

    public function test_startUpdate创建状态文件并返回run_id(): void
    {
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $res = $service->startUpdate();

        $this->assertEquals(0, $res['code'], $res['message'] ?? '');
        $this->assertNotEmpty($res['data']['run_id']);
        $this->assertEquals('1.1.0', $res['data']['latest_version']);
        $this->assertEquals(strlen('NEW-BINARY-CONTENT'), $res['data']['total_size']);

        $state = json_decode((string) file_get_contents($this->statePath()), true);
        $this->assertEquals('download', $state['step']);
        $this->assertEquals($res['data']['run_id'], $state['run_id']);
        $this->assertTrue($state['was_online']);
    }

    public function test_进行中任务60秒内重复发起返回50014(): void
    {
        // 手动写入 60 秒内有心跳的进行中状态
        @mkdir(dirname($this->statePath()), 0755, true);
        file_put_contents($this->statePath(), json_encode([
            'run_id' => 'abc123',
            'step' => 'download',
            'updated_at' => date('Y-m-d H:i:s'),
        ]));
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $res = $service->startUpdate();

        $this->assertEquals(50014, $res['code']);
        // data 中携带现有任务进度
        $this->assertEquals('abc123', $res['data']['run_id']);
    }

    public function test_stepRunId不匹配时返回50015(): void
    {
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $res = $service->startUpdate();
        $stepRes = $service->stepUpdate('wrong-run-id');

        $this->assertEquals(50015, $stepRes['code']);
    }

    public function test_status无任务时返回null(): void
    {
        $service = $this->makeService();

        $res = $service->updateStatus();

        $this->assertEquals(0, $res['code']);
        $this->assertNull($res['data']);
    }

    // ============ download 步骤 ============

    public function test_download步完成校验后转为replace(): void
    {
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $res = $service->startUpdate();
        $stepRes = $service->stepUpdate($res['data']['run_id']);

        $this->assertEquals(0, $stepRes['code'], $stepRes['message'] ?? '');
        $this->assertEquals('replace', $stepRes['data']['step']);
        // 临时文件内容与远程一致
        $state = json_decode((string) file_get_contents($this->statePath()), true);
        $this->assertEquals('NEW-BINARY-CONTENT', file_get_contents($state['tmp_path']));
        // 未修改原二进制
        $this->assertEquals('OLD-BINARY-CONTENT', file_get_contents($binary));
    }

    public function test_download校验失败时任务失败(): void
    {
        // 下载内容大小正确但内容与 checksum 不符（触发 SHA256 校验失败分支）
        $brokenContent = 'BROKEN-CONTENT';
        $this->fakeHttp([
            'download' => $brokenContent,
            'latest' => [
                'code' => 0,
                'data' => [
                    'version' => '1.1.0',
                    'changelog' => '',
                    'release_date' => '2026-09-24 12:00:00',
                    'file_size' => strlen($brokenContent),
                    'checksum' => hash('sha256', 'NEW-BINARY-CONTENT'),
                    'download_url' => '/api/versionsvr/local-service/download/1.1.0?platform=windows',
                ],
            ],
        ]);
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $res = $service->startUpdate();
        $stepRes = $service->stepUpdate($res['data']['run_id']);

        $this->assertEquals(500, $stepRes['code']);
        $this->assertEquals('failed', $stepRes['data']['step']);
        $this->assertStringContainsString('SHA256', $stepRes['data']['message']);
        // 未修改原二进制
        $this->assertEquals('OLD-BINARY-CONTENT', file_get_contents($binary));
    }

    public function test_download内容为空时任务失败(): void
    {
        $this->fakeHttp(['download' => '']);
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $res = $service->startUpdate();
        $stepRes = $service->stepUpdate($res['data']['run_id']);

        $this->assertEquals(500, $stepRes['code']);
        $this->assertEquals('failed', $stepRes['data']['step']);
    }

    // ============ replace 步骤 ============

    public function test_replace停服失败时中止且不修改文件(): void
    {
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $this->mockManager($binary, ['code' => 403, 'message' => '权限不足']);
        $service = $this->makeService();

        $runId = $this->startAndDownload($service);
        $stepRes = $service->stepUpdate($runId);

        $this->assertEquals(403, $stepRes['code']);
        $this->assertEquals('failed', $stepRes['data']['step']);
        $this->assertEquals('OLD-BINARY-CONTENT', file_get_contents($binary));
        $this->assertFileDoesNotExist($binary . '.bak');
    }

    public function test_replace成功后转入verify(): void
    {
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $runId = $this->startAndDownload($service);
        $stepRes = $service->stepUpdate($runId);

        $this->assertEquals('verify', $stepRes['data']['step']);
        // 二进制已替换为新内容，旧版本备份保留待验证
        $this->assertEquals('NEW-BINARY-CONTENT', file_get_contents($binary));
        $this->assertFileExists($binary . '.bak');
    }

    public function test_服务离线时replace直接finished不启动服务(): void
    {
        $this->fakeHttp(['status' => ['code' => 503, 'message' => '本地服务不可用']]);
        $binary = $this->fakeBinary();
        $manager = $this->mockManager($binary);
        $manager->shouldNotReceive('stop');
        $manager->shouldNotReceive('start');
        $service = $this->makeService();

        $runId = $this->startAndDownload($service);
        $stepRes = $service->stepUpdate($runId);

        $this->assertEquals(0, $stepRes['code'], $stepRes['message'] ?? '');
        $this->assertEquals('finished', $stepRes['data']['step']);
        $this->assertEquals('NEW-BINARY-CONTENT', file_get_contents($binary));
        $this->assertStringContainsString('请手动启动', $stepRes['data']['message']);
    }

    public function test_延迟启动已布置时跳过主动启动(): void
    {
        // 模拟经 Go 降级停止：stop 返回 delayed_start=true，
        // start 必须以 delayedStartPlanned=true 调用（内部短路返回成功，不再执行启动命令）
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $manager = \Mockery::mock(ServiceManager::class)->makePartial()->shouldAllowMockingProtectedMethods();
        $manager->shouldReceive('currentBinaryPath')->andReturn($binary);
        $manager->shouldReceive('stop')->with(true)->andReturn(['code' => 0, 'message' => 'success', 'data' => ['output' => '', 'delayed_start' => true]]);
        $manager->shouldReceive('start')->with(true)->andReturn(['code' => 0, 'message' => '延迟启动进程已布置，等待服务自动拉起', 'data' => ['delayed_start' => true]]);
        $this->app->instance(ServiceManager::class, $manager);
        $service = $this->makeService();

        $runId = $this->startAndDownload($service);
        $stepRes = $service->stepUpdate($runId); // replace → verify

        $this->assertEquals('verify', $stepRes['data']['step']);
        $this->assertEquals('NEW-BINARY-CONTENT', file_get_contents($binary));
    }

    // ============ verify 步骤 ============

    public function test_verify上线后finished并清理临时文件与备份(): void
    {
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $runId = $this->startAndDownload($service);
        $service->stepUpdate($runId); // replace → verify
        $stepRes = $service->stepUpdate($runId); // verify 第 1 轮探测在线

        $this->assertEquals(0, $stepRes['code'], $stepRes['message'] ?? '');
        $this->assertEquals('finished', $stepRes['data']['step']);
        $this->assertStringContainsString('更新完成', $stepRes['data']['message']);
        // 临时文件与备份已清理
        $this->assertFileDoesNotExist($binary . '.bak');
        $updateDir = storage_path('app/localservice/update');
        $this->assertCount(0, glob($updateDir . '/*.tmp'));
    }

    public function test_verify五轮未上线时回滚旧版本(): void
    {
        // status 按请求次序返回：start 阶段在线 1 次，verify 阶段离线 5 次
        $seq = Http::sequence()
            ->push(['code' => 0, 'data' => ['version' => '1.0.0', 'status' => 'running']])
            ->push(['code' => 503, 'message' => '本地服务不可用'])
            ->push(['code' => 503, 'message' => '本地服务不可用'])
            ->push(['code' => 503, 'message' => '本地服务不可用'])
            ->push(['code' => 503, 'message' => '本地服务不可用'])
            ->push(['code' => 503, 'message' => '本地服务不可用'])
            ->whenEmpty(Http::response(['code' => 503, 'message' => '本地服务不可用']));
        $this->fakeHttp(['status' => $seq]);
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $runId = $this->startAndDownload($service);
        $service->stepUpdate($runId); // replace → verify

        $stepRes = null;
        for ($i = 0; $i < 5; $i++) {
            $stepRes = $service->stepUpdate($runId);
        }

        $this->assertEquals(50012, $stepRes['code']);
        $this->assertEquals('failed', $stepRes['data']['step']);
        // 已回滚为旧内容
        $this->assertEquals('OLD-BINARY-CONTENT', file_get_contents($binary));
    }

    public function test_verify终态后重复轮询幂等返回(): void
    {
        $this->fakeHttp();
        $binary = $this->fakeBinary();
        $this->mockManager($binary);
        $service = $this->makeService();

        $runId = $this->startAndDownload($service);
        $service->stepUpdate($runId);
        $service->stepUpdate($runId); // finished

        // 终态后重复调用：幂等返回 finished，不报错
        $stepRes = $service->stepUpdate($runId);

        $this->assertEquals(0, $stepRes['code']);
        $this->assertEquals('finished', $stepRes['data']['step']);
    }
}
