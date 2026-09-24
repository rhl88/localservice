<?php

namespace App\Apps\CmsproLocalservice\Services;

use App\Http\Responses\ApiResponse;
use Illuminate\Support\Facades\File;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Str;

/**
 * 本地服务在线更新服务。
 *
 * 从版本服务器（Versionsvr 应用，https://v5.cmspro.cn）检查并下载
 * 当前平台的 Go 服务二进制，校验后停服替换二进制并重启服务；
 * 启动失败时自动回滚到旧版本，保证服务可用性。
 */
class UpdateService
{
    /** 版本服务器基础地址 */
    public const SERVER_BASE_URL = 'https://v5.cmspro.cn';

    public function __construct(
        protected ServiceManager $manager,
        protected LocalServiceClient $client
    ) {}

    /**
     * 当前平台标识（与版本服务器的平台字段约定一致）。
     */
    public function platform(): string
    {
        if (PHP_OS_FAMILY === 'Windows') {
            return 'windows';
        }
        return in_array(php_uname('m'), ['aarch64', 'arm64'], true) ? 'linux-arm64' : 'linux';
    }

    /**
     * 当前运行中 Go 服务的版本号（服务离线时为 null）。
     */
    public function currentVersion(): ?string
    {
        $res = $this->client->status();
        if (($res['code'] ?? -1) === 0) {
            return $res['data']['version'] ?? null;
        }
        return null;
    }

    /**
     * 检查更新。
     *
     * @return array{code:int, message:string, data?:array}
     *         data: {current_version, latest_version, has_update, changelog, release_date, file_size}
     */
    public function checkUpdate(): array
    {
        $current = $this->currentVersion();
        if ($current === null) {
            return ApiResponse::error(503, '本地服务未运行，无法检查更新');
        }

        $latestRes = $this->fetchLatest();
        if ($latestRes['code'] !== 0) {
            return $latestRes;
        }

        $latest = $latestRes['data'];

        return ApiResponse::success([
            'current_version' => $current,
            'latest_version' => $latest['version'],
            'has_update' => version_compare($latest['version'], $current, '>'),
            'changelog' => $latest['changelog'] ?? '',
            'release_date' => $latest['release_date'] ?? '',
            'file_size' => $latest['file_size'] ?? 0,
        ]);
    }

    /**
     * 发起更新：校验环境并创建更新任务状态文件，返回 run_id 供前端分步驱动。
     *
     * 长任务状态机设计：下载/替换/验证拆分为多个短请求由前端轮询推进，
     * 避免单请求内同步执行「下载几十MB→停服→替换→重启」被网关超时中断（502）。
     *
     * @return array{code:int, message:string, data?:array}
     *         成功 data: {run_id, current_version, latest_version, total_size, changelog}
     */
    public function startUpdate(): array
    {
        // 已有进行中的任务（60 秒内有心跳）则拒绝重复发起，返回现有进度
        $existing = $this->readState();
        if ($existing !== null && !in_array($existing['step'], ['finished', 'failed'], true)) {
            if ((time() - strtotime($existing['updated_at'])) < 60) {
                return ApiResponse::error(50014, '更新任务进行中，请勿重复发起', $this->publicState($existing));
            }
        }

        $current = $this->currentVersion();
        $wasOnline = $current !== null;

        $latestRes = $this->fetchLatest();
        if ($latestRes['code'] !== 0) {
            return $latestRes;
        }
        $latest = $latestRes['data'];

        if ($current !== null && !version_compare($latest['version'], $current, '>')) {
            return ApiResponse::error(50010, '当前已是最新版本（' . $current . '）');
        }

        $binary = $this->manager->currentBinaryPath();
        if (!is_file($binary)) {
            return ApiResponse::error(50001, '未找到本地服务二进制文件：' . $binary);
        }

        // 下载临时文件用固定文件名（便于断点续传），重新发起时清空重下
        $tmpDir = storage_path('app' . DIRECTORY_SEPARATOR . 'localservice' . DIRECTORY_SEPARATOR . 'update');
        if (!is_dir($tmpDir)) {
            @mkdir($tmpDir, 0755, true);
        }
        $tmpPath = $tmpDir . DIRECTORY_SEPARATOR . 'local-service-' . $this->platform() . '.tmp';
        @unlink($tmpPath);

        $state = [
            'run_id' => Str::random(16),
            'step' => 'download',
            'downloaded_bytes' => 0,
            'download_retries' => 0,
            'tmp_path' => $tmpPath,
            'expected_size' => (int) ($latest['file_size'] ?? 0),
            'checksum' => $latest['checksum'] ?? '',
            'version' => $latest['version'],
            'download_url' => $latest['download_url'],
            'current_version' => $current,
            'was_online' => $wasOnline,
            'binary' => $binary,
            'backup_path' => $binary . '.bak',
            'verify_round' => 0,
            'error_code' => 0,
            'message' => '准备下载',
            'updated_at' => date('Y-m-d H:i:s'),
        ];
        $this->saveState($state);

        return ApiResponse::success([
            'run_id' => $state['run_id'],
            'current_version' => $current,
            'latest_version' => $state['version'],
            'total_size' => $state['expected_size'],
            'changelog' => $latest['changelog'] ?? '',
        ]);
    }

    /**
     * 执行更新任务的下一步（download / replace / verify 单步）。
     * 每次调用只推进一个可短时完成的工作单元，前端循环调用直至 finished/failed。
     *
     * @return array{code:int, message:string, data?:array}
     *         data: {run_id, step, version, downloaded_bytes, expected_size, progress, verify_round, error_code, message}
     */
    public function stepUpdate(string $runId): array
    {
        $state = $this->readState();
        if ($state === null || ($state['run_id'] ?? '') !== $runId) {
            return ApiResponse::error(50015, '更新任务不存在或已失效，请重新发起更新');
        }

        // 已终态：幂等返回，防止前端重复轮询报错
        if (in_array($state['step'], ['finished', 'failed'], true)) {
            return ApiResponse::success($this->publicState($state));
        }

        try {
            $res = match ($state['step']) {
                'download' => $this->stepDownload($state),
                'replace' => $this->stepReplace($state),
                'verify' => $this->stepVerify($state),
                default => $this->failState($state, 50015, '未知更新步骤：' . $state['step']),
            };
        } catch (\Throwable $e) {
            $res = $this->failState($state, 500, '更新过程异常：' . $e->getMessage());
        }

        return $res;
    }

    /**
     * 查询更新任务状态（无任务时 data 为 null，供页面刷新后恢复轮询）。
     */
    public function updateStatus(): array
    {
        $state = $this->readState();
        return ApiResponse::success($state !== null ? $this->publicState($state) : null);
    }

    /**
     * 下载步骤：单时间片下载（超时中断保留已下载内容，下一轮断点续传），
     * 下载完整后执行大小 / SHA256 校验并转入 replace 步骤。
     */
    protected function stepDownload(array &$state): array
    {
        $chunkPath = $state['tmp_path'] . '.chunk';
        $interrupted = false;
        $statusCode = 0;

        // 单时间片限制在 15 秒内：超时抛异常即中断本轮，已写入 chunk 的内容不丢弃
        try {
            $http = Http::timeout(15)->withOptions(['verify' => false, 'sink' => $chunkPath]);
            if ($state['downloaded_bytes'] > 0) {
                $http = $http->withHeaders(['Range' => 'bytes=' . $state['downloaded_bytes'] . '-']);
            }
            $response = $http->get(self::SERVER_BASE_URL . $state['download_url']);
            $statusCode = $response->status();
        } catch (\Throwable $e) {
            $interrupted = true;
        }

        // 合并本轮分片到主临时文件
        if (is_file($chunkPath)) {
            $chunkSize = filesize($chunkPath);
            if ($chunkSize > 0) {
                if ($statusCode === 200 && $state['downloaded_bytes'] > 0) {
                    // 服务器不支持断点续传（返回全量 200），覆盖重下
                    @rename($chunkPath, $state['tmp_path']);
                    $state['downloaded_bytes'] = $chunkSize;
                } else {
                    // 206 断点续传（或首轮全量 200），追加到主文件
                    $this->appendFile($chunkPath, $state['tmp_path']);
                    $state['downloaded_bytes'] += $chunkSize;
                    @unlink($chunkPath);
                }
            }
        }

        $done = $state['expected_size'] > 0 && $state['downloaded_bytes'] >= $state['expected_size'];

        // 中断且尚未完成：记录进度等待下一轮；连续多次零进展则判定失败
        if ($interrupted && !$done) {
            $hasProgress = (is_file($chunkPath) && filesize($chunkPath) > 0) || $state['downloaded_bytes'] > 0;
            if ($hasProgress) {
                $state['download_retries'] = 0;
            } else {
                $state['download_retries'] += 1;
                if ($state['download_retries'] > 5) {
                    return $this->failState($state, 500, '下载连续多次无进展，请稍后重试');
                }
            }
            $state['message'] = '下载中';
            $this->saveState($state);
            return ApiResponse::success($this->publicState($state));
        }

        return $this->finishDownload($state);
    }

    /**
     * 下载完整性校验（JSON 头 / 大小 / SHA256），通过后转入 replace 步骤。
     */
    protected function finishDownload(array &$state): array
    {
        $tmpPath = $state['tmp_path'];
        if (!is_file($tmpPath) || filesize($tmpPath) === 0) {
            return $this->failState($state, 500, '下载内容为空');
        }

        // 业务错误 JSON 可能被保存为文件（HTTP 200 + 错误体），先按头两字节识别
        $head = (string) file_get_contents($tmpPath, false, null, 0, 2);
        if (str_starts_with($head, '{')) {
            $json = json_decode((string) file_get_contents($tmpPath), true);
            return $this->failState($state, 500, '下载到的是错误响应而非服务文件：' . ($json['message'] ?? '未知错误'));
        }

        if ($state['expected_size'] > 0 && filesize($tmpPath) !== $state['expected_size']) {
            return $this->failState($state, 500, '文件大小校验失败（期望 ' . $state['expected_size'] . ' 字节，实际 ' . filesize($tmpPath) . ' 字节）');
        }

        if ($state['checksum'] !== '' && hash_file('sha256', $tmpPath) !== $state['checksum']) {
            return $this->failState($state, 500, '文件 SHA256 校验失败，下载内容可能已损坏');
        }

        $state['message'] = '下载完成，准备替换';
        $state['step'] = 'replace';
        $this->saveState($state);
        return ApiResponse::success($this->publicState($state));
    }

    /**
     * 替换步骤：停服（在线时）→ 备份 → 替换 → 启动发起；服务本就离线时仅替换文件。
     * exec 不可用环境下降级经 Go 服务停服，并布置延迟启动进程自动拉起新版本。
     */
    protected function stepReplace(array &$state): array
    {
        // 服务在线时先停止（withDelayedStart=true：仅经 Go 降级停止时布置延迟启动）
        $delayedStart = false;
        if ($state['was_online']) {
            $stopRes = $this->manager->stop(true);
            if ($stopRes['code'] !== 0) {
                @unlink($state['tmp_path']);
                return $this->failState($state, $stopRes['code'], '停止服务失败：' . $stopRes['message'] . '。已中止更新，未修改任何文件。');
            }
            $delayedStart = (bool) ($stopRes['data']['delayed_start'] ?? false);
            $this->waitProcessExit();
        }

        // 备份旧二进制 → 替换
        if (!@copy($state['binary'], $state['backup_path'])) {
            @unlink($state['tmp_path']);
            return $this->failState($state, 50011, '备份旧版本失败，已中止更新');
        }

        if (!$this->replaceBinary($state['binary'], $state['tmp_path'])) {
            // 替换失败且原文件缺失时从备份恢复（服务停止中，缺文件将无法拉起）
            if (!is_file($state['binary'])) {
                @copy($state['backup_path'], $state['binary']);
            }
            @unlink($state['tmp_path']);
            return $this->failState($state, 50011, '替换二进制文件失败（目录可能不可写或进程文件锁未释放），旧版本已保留备份：' . $state['backup_path']);
        }
        if (PHP_OS_FAMILY !== 'Windows') {
            @chmod($state['binary'], 0755);
        }

        if (!$state['was_online']) {
            // 服务本就离线：仅替换文件，无需启动验证
            $state['step'] = 'finished';
            $state['message'] = '服务文件已替换为 ' . $state['version'] . '（服务当前离线，请手动启动）';
            $this->saveState($state);
            return ApiResponse::success($this->publicState($state));
        }

        // 发起启动命令（延迟启动进程已布置时跳过，由其自动拉起）；启动失败则立即回滚
        $startRes = $this->manager->start($delayedStart);
        if ($startRes['code'] !== 0) {
            $this->rollback($state['binary'], $state['backup_path'], true);
            @unlink($state['tmp_path']);
            return $this->failState($state, 50012, '新版本启动失败，已回滚旧版本：' . $startRes['message']);
        }

        $state['step'] = 'verify';
        $state['verify_round'] = 0;
        $state['message'] = '替换完成，正在启动新版本';
        $this->saveState($state);
        return ApiResponse::success($this->publicState($state));
    }

    /**
     * 替换二进制文件：copy 失败时（Windows 下旧进程文件锁未完全释放）rename 腾出路径后重试，
     * 仍失败则恢复原文件路径，保证磁盘上始终存在可用二进制。
     */
    protected function replaceBinary(string $binary, string $tmpPath): bool
    {
        if (@copy($tmpPath, $binary)) {
            return true;
        }
        if (!@rename($binary, $binary . '.replacing')) {
            return false;
        }
        if (@copy($tmpPath, $binary)) {
            @unlink($binary . '.replacing');
            return true;
        }
        // 仍失败：把原文件移回原路径
        @rename($binary . '.replacing', $binary);
        return false;
    }

    /**
     * 验证步骤：单次调用仅做一轮健康探测（间隔 3 秒），
     * 连续 5 轮未上线则回滚旧版本并标记失败。
     */
    protected function stepVerify(array &$state): array
    {
        $state['verify_round'] += 1;
        $this->probeDelay();
        $this->saveState($state);

        if ($this->currentVersion() !== null) {
            @unlink($state['tmp_path']);
            @unlink($state['backup_path']);
            $state['step'] = 'finished';
            $state['message'] = '更新完成，已升级到 ' . $state['version'];
            $this->saveState($state);
            return ApiResponse::success($this->publicState($state));
        }

        if ($state['verify_round'] < 5) {
            $state['message'] = '启动验证中（第 ' . $state['verify_round'] . '/5 轮）';
            $this->saveState($state);
            return ApiResponse::success($this->publicState($state));
        }

        // 5 轮未上线：回滚旧版本
        $this->rollback($state['binary'], $state['backup_path'], true);
        @unlink($state['tmp_path']);
        return $this->failState($state, 50012, '新版本启动失败，已回滚旧版本');
    }

    /**
     * 更新状态文件路径。
     */
    protected function statePath(): string
    {
        return storage_path('app' . DIRECTORY_SEPARATOR . 'localservice' . DIRECTORY_SEPARATOR . 'update' . DIRECTORY_SEPARATOR . 'update_state.json');
    }

    /**
     * 读取更新状态文件（不存在或非法时返回 null）。
     */
    protected function readState(): ?array
    {
        $path = $this->statePath();
        if (!is_file($path)) {
            return null;
        }
        $state = json_decode((string) file_get_contents($path), true);
        return is_array($state) ? $state : null;
    }

    /**
     * 写入更新状态文件并刷新心跳时间。
     */
    protected function saveState(array $state): void
    {
        $state['updated_at'] = date('Y-m-d H:i:s');
        @file_put_contents($this->statePath(), json_encode($state, JSON_PRETTY_PRINT | JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES));
    }

    /**
     * 标记任务失败：持久化失败状态并返回错误响应。
     */
    protected function failState(array &$state, int $code, string $message): array
    {
        $state['step'] = 'failed';
        $state['error_code'] = $code;
        $state['message'] = $message;
        $this->saveState($state);
        return ApiResponse::error($code, $message, $this->publicState($state));
    }

    /**
     * 提取前端可见的状态子集（隐藏文件路径等内部信息）。
     */
    protected function publicState(array $state): array
    {
        return [
            'run_id' => $state['run_id'] ?? '',
            'step' => $state['step'] ?? 'download',
            'version' => $state['version'] ?? '',
            'downloaded_bytes' => (int) ($state['downloaded_bytes'] ?? 0),
            'expected_size' => (int) ($state['expected_size'] ?? 0),
            'progress' => ($state['expected_size'] ?? 0) > 0
                ? min(100, (int) round(($state['downloaded_bytes'] ?? 0) / $state['expected_size'] * 100))
                : null,
            'verify_round' => (int) ($state['verify_round'] ?? 0),
            'error_code' => (int) ($state['error_code'] ?? 0),
            'message' => $state['message'] ?? '',
        ];
    }

    /**
     * 将分片文件内容追加到目标文件末尾。
     */
    protected function appendFile(string $source, string $target): void
    {
        $src = @fopen($source, 'rb');
        if ($src === false) {
            return;
        }
        $dst = @fopen($target, 'ab');
        if ($dst === false) {
            fclose($src);
            return;
        }
        stream_copy_to_stream($src, $dst);
        fclose($src);
        fclose($dst);
    }

    /**
     * 验证探测间隔（测试替身可覆写消除 sleep）。
     */
    protected function probeDelay(): void
    {
        sleep(3);
    }

    /**
     * 拉取版本服务器最新已发布版本信息（按当前平台）。
     */
    protected function fetchLatest(): array
    {
        try {
            $res = Http::timeout(15)
                ->withOptions(['verify' => false])
                ->acceptJson()
                ->get(self::SERVER_BASE_URL . '/api/versionsvr/local-service/latest', [
                    'platform' => $this->platform(),
                ]);
        } catch (\Throwable $e) {
            return ApiResponse::error(500, '连接版本服务器失败：' . $e->getMessage());
        }

        $json = $res->json();
        if (!is_array($json)) {
            return ApiResponse::error(500, '版本服务器返回非法响应');
        }

        // 业务码 50006（暂无已发布版本）属于正常业务状态，原样透出
        if (($json['code'] ?? -1) !== 0) {
            return ApiResponse::error($json['code'] ?? 500, $json['message'] ?? '版本服务器返回错误');
        }

        return ApiResponse::success($json['data'] ?? []);
    }

    /**
     * 等待旧服务进程退出（Windows 停止服务后进程释放文件锁存在延迟）。
     */
    protected function waitProcessExit(): void
    {
        sleep(2);
    }

    /**
     * 回滚：恢复备份的二进制并重新启动旧版本服务。
     */
    protected function rollback(string $binary, string $backupPath, bool $wasOnline): void
    {
        if (!is_file($backupPath)) {
            return;
        }
        @copy($backupPath, $binary);
        if (PHP_OS_FAMILY !== 'Windows') {
            @chmod($binary, 0755);
        }
        if ($wasOnline) {
            $this->manager->start();
        }
    }
}
