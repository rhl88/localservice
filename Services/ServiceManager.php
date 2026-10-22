<?php

namespace App\Apps\CmsproLocalservice\Services;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;

/**
 * 本地服务生命周期管理（Windows/Linux 双平台）。
 * Windows: 通过 sc 命令管理服务，生成 .bat 脚本由用户以管理员身份运行。
 * Linux: 通过 systemctl 管理 systemd 服务，生成 .sh 脚本由用户以 root 身份运行。
 * 运行状态检测统一走 HTTP /api/status。
 */
class ServiceManager
{
    /** 本应用配置项缓存（code 去前缀后的 name => value），首次访问时惰性加载 */
    private ?array $configCache = null;

    /**
     * 当前平台是否为 Windows。
     */
    protected function isWindows(): bool
    {
        return PHP_OS_FAMILY === 'Windows';
    }

    /**
     * 安全执行 shell 命令。
     * 生产环境（如宝塔）常通过 disable_functions 禁用 shell_exec/exec，
     * 被禁函数调用会抛 "Call to undefined function" 致命错误（@ 抑制无效），
     * 因此先经 function_exists 检测。
     *
     * 优先 exec（可拿到可靠的退出码）；仅 exec 不可用时走 shell_exec（退出码未知，以 -1 标记）。
     * 注意：输出为空不代表失败——systemctl start/stop/restart 等命令成功时静默无输出。
     *
     * @return array|null null = 两个函数均不可用（无法执行）；
     *         array{output: string, exit_code: int} 已执行，exit_code：0 成功 / >0 失败 / -1 未知。
     */
    protected function safeShellExec(string $cmd): ?array
    {
        if (function_exists('exec')) {
            $lines = [];
            $exitCode = -1;
            @exec($cmd, $lines, $exitCode);
            return ['output' => implode("\n", $lines), 'exit_code' => (int) $exitCode];
        }
        if (function_exists('shell_exec')) {
            $output = @shell_exec($cmd);
            return ['output' => (string) ($output ?? ''), 'exit_code' => -1];
        }
        return null;
    }

    /**
     * 应用根目录绝对路径。
     */
    protected function appPath(): string
    {
        return base_path('app/Apps/CmsproLocalservice');
    }

    /**
     * Go 二进制绝对路径（按平台选择）。
     */
    protected function binaryPath(): string
    {
        if ($this->isWindows()) {
            $rel = config('apps.cmspro.localservice.binary', 'Bin/windows/cmspro-local-service.exe');
        } else {
            $rel = config('apps.cmspro.localservice.binary_linux', 'Bin/linux/cmspro-local-service');
        }
        return $this->appPath() . DIRECTORY_SEPARATOR . str_replace('/', DIRECTORY_SEPARATOR, $rel);
    }

    /**
     * 当前平台实际使用的 Go 二进制路径（含 Linux arm64 架构判定），
     * 供更新服务定位待替换文件。与安装包装脚本的架构选择逻辑保持一致。
     */
    public function currentBinaryPath(): string
    {
        if ($this->isWindows()) {
            return $this->binaryPath();
        }
        if (in_array(php_uname('m'), ['aarch64', 'arm64'], true)) {
            return $this->appPath() . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'linux' . DIRECTORY_SEPARATOR . 'cmspro-local-service-arm64';
        }
        return $this->binaryPath();
    }

    /**
     * 脚本输出目录（storage/app/localservice）。
     */
    protected function scriptDir(): string
    {
        $dir = storage_path('app' . DIRECTORY_SEPARATOR . 'localservice');
        if (!is_dir($dir)) {
            @mkdir($dir, 0755, true);
        }
        return $dir;
    }

    protected function serviceName(): string
    {
        return config('apps.cmspro.localservice.service_name', 'CmsProLocalService');
    }

    protected function serviceDisplayName(): string
    {
        return config('apps.cmspro.localservice.service_display_name', 'CmsPro 本地服务');
    }

    /**
     * 生成安装脚本，返回脚本路径与内容。
     * Windows: 调用 Go 二进制的 install 子命令注册 Windows 服务。
     * Linux: 生成 systemd unit 文件并通过 systemctl 注册服务。
     * 生成前自动同步二进制为版本服务器最新已发布版本（无网络时降级使用本地文件）。
     *
     * @return array{code:int, message:string, data?:array}
     */
    public function installScript(): array
    {
        // 当前平台实际使用的二进制（Linux arm64 自动切换 arm64 文件，与安装包装脚本架构选择一致）
        $binary = $this->currentBinaryPath();
        $updateResult = $this->ensureBinaryUpToDate($binary);
        if ($updateResult['code'] !== 0) {
            return $updateResult;
        }

        $name = $this->serviceName();
        $display = $this->serviceDisplayName();
        $this->syncConfig();

        if ($this->isWindows()) {
            $content = "@echo off\r\n"
                . "chcp 65001 >nul\r\n"
                . "echo 正在注册 CmsPro 本地服务...\r\n"
                . "\"" . $binary . "\" install\r\n"
                . "if %errorlevel% neq 0 (\r\n"
                . "  echo 注册失败，请确认以管理员身份运行本脚本。\r\n"
                . "  pause\r\n"
                . "  exit /b 1\r\n"
                . ")\r\n"
                . "echo 注册成功，正在启动服务...\r\n"
                . "sc start \"" . $name . "\"\r\n"
                . "echo 完成。\r\n"
                . "pause\r\n";
            $path = $this->scriptDir() . DIRECTORY_SEPARATOR . 'install-service.bat';
        } else {
            // Linux: 生成包装脚本 + systemd unit 文件 + 安装脚本
            $configPath = storage_path('local_service_config.json');

            // 生成包装脚本：通过环境变量 LOCAL_SERVICE_CONFIG_PATH 传递配置文件路径，
            // 绕过 WSL /init 代理执行下 os.Executable() 返回路径错误的问题；
            // 并根据 uname -m 自动选择对应架构的二进制。
            $binaryDir = dirname($binary);
            $binaryName = basename($binary);
            $wrapperPath = $this->scriptDir() . DIRECTORY_SEPARATOR . $name . '-wrapper.sh';
            $wrapperContent = "#!/bin/bash\n"
                . "# CmsPro 本地服务启动包装脚本\n"
                . "# 自动检测架构 + 通过环境变量传递配置路径\n"
                . "set -e\n\n"
                . "ARCH=\"\$(uname -m)\"\n"
                . "BIN_DIR=\"" . $binaryDir . "\"\n"
                . "case \"\$ARCH\" in\n"
                . "  aarch64|arm64)\n"
                . "    BIN=\"\$BIN_DIR/" . $binaryName . "-arm64\"\n"
                . "    ;;\n"
                . "  *)\n"
                . "    BIN=\"\$BIN_DIR/" . $binaryName . "\"\n"
                . "    ;;\n"
                . "esac\n\n"
                . "if [ ! -f \"\$BIN\" ]; then\n"
                . "  echo \"错误：未找到二进制文件 \$BIN\" >&2\n"
                . "  exit 1\n"
                . "fi\n\n"
                . "export LOCAL_SERVICE_CONFIG_PATH=\"" . $configPath . "\"\n"
                . "cd \"" . base_path() . "\"\n"
                . "exec \"\$BIN\" run\n";
            file_put_contents($wrapperPath, $wrapperContent);
            @chmod($wrapperPath, 0755);

            $unitContent = "[Unit]\n"
                . "Description=" . $display . "\n"
                . "After=network.target\n\n"
                . "[Service]\n"
                . "Type=simple\n"
                . "WorkingDirectory=" . base_path() . "\n"
                . "ExecStart=" . $wrapperPath . "\n"
                . "Restart=on-failure\n"
                . "RestartSec=5\n\n"
                . "[Install]\n"
                . "WantedBy=multi-user.target\n";
            $unitPath = $this->scriptDir() . DIRECTORY_SEPARATOR . $name . '.service';
            file_put_contents($unitPath, $unitContent);

            $content = "#!/bin/bash\n"
                . "# CmsPro 本地服务安装脚本（需要 root 权限）\n"
                . "set -e\n\n"
                . "if [ \"\$EUID\" -ne 0 ]; then\n"
                . "  echo \"请使用 root 权限运行：sudo bash install-service.sh\"\n"
                . "  exit 1\n"
                . "fi\n\n"
                . "SERVICE_NAME=\"" . $name . "\"\n"
                . "SYSTEMD_DIR=/etc/systemd/system\n"
                . "UNIT_FILE=\"\$SYSTEMD_DIR/\$SERVICE_NAME.service\"\n\n"
                . $this->systemdLockSnippet()
                . "echo \"正在安装 CmsPro 本地服务...\"\n\n"
                . "# 确保二进制和包装脚本有执行权限\n"
                . "chmod +x \"" . $binary . "\" \"" . $wrapperPath . "\"\n\n"
                . "unlockSystemdDir\n\n"
                . "# 生成 systemd unit 文件\n"
                . "cp \"" . $unitPath . "\" \"\$UNIT_FILE\"\n"
                . "systemctl daemon-reload\n"
                . "systemctl enable \"\$SERVICE_NAME\"\n"
                . "systemctl start \"\$SERVICE_NAME\"\n\n"
                . "restoreSystemdDir\n\n"
                . "echo \"安装完成，服务状态：\"\n"
                . "systemctl status \"\$SERVICE_NAME\" --no-pager\n";
            $path = $this->scriptDir() . DIRECTORY_SEPARATOR . 'install-service.sh';
        }
        file_put_contents($path, $content);
        if (!$this->isWindows()) {
            @chmod($path, 0755);
        }

        return [
            'code' => 0,
            'message' => 'success',
            'data' => [
                'script_path' => $path,
                'binary_path' => $binary,
                'service_name' => $name,
                'display_name' => $display,
            ],
        ];
    }

    /**
     * 生成卸载脚本。
     */
    public function uninstallScript(): array
    {
        $binary = $this->binaryPath();
        if (!is_file($binary)) {
            $downloadResult = $this->downloadBinary();
            if ($downloadResult['code'] !== 0) {
                return $downloadResult;
            }
        }

        $name = $this->serviceName();

        if ($this->isWindows()) {
            $content = "@echo off\r\n"
                . "chcp 65001 >nul\r\n"
                . "echo 正在停止并卸载 CmsPro 本地服务...\r\n"
                . "sc stop \"" . $name . "\"\r\n"
                . "\"" . $binary . "\" uninstall\r\n"
                . "if %errorlevel% neq 0 (\r\n"
                . "  echo 卸载失败，请确认以管理员身份运行本脚本。\r\n"
                . "  pause\r\n"
                . "  exit /b 1\r\n"
                . ")\r\n"
                . "echo 卸载完成。\r\n"
                . "pause\r\n";
            $path = $this->scriptDir() . DIRECTORY_SEPARATOR . 'uninstall-service.bat';
        } else {
            $content = "#!/bin/bash\n"
                . "# CmsPro 本地服务卸载脚本（需要 root 权限）\n"
                . "set -e\n\n"
                . "if [ \"\$EUID\" -ne 0 ]; then\n"
                . "  echo \"请使用 root 权限运行：sudo bash uninstall-service.sh\"\n"
                . "  exit 1\n"
                . "fi\n\n"
                . "SERVICE_NAME=\"" . $name . "\"\n"
                . "SYSTEMD_DIR=/etc/systemd/system\n"
                . "UNIT_FILE=\"\$SYSTEMD_DIR/\$SERVICE_NAME.service\"\n\n"
                . $this->systemdLockSnippet()
                . "echo \"正在卸载 CmsPro 本地服务...\"\n\n"
                . "systemctl stop \"\$SERVICE_NAME\" 2>/dev/null || true\n"
                . "systemctl disable \"\$SERVICE_NAME\" 2>/dev/null || true\n\n"
                . "unlockSystemdDir\n"
                . "rm -f \"\$UNIT_FILE\"\n"
                . "restoreSystemdDir\n"
                . "systemctl daemon-reload\n\n"
                . "echo \"卸载完成。\"\n";
            $path = $this->scriptDir() . DIRECTORY_SEPARATOR . 'uninstall-service.sh';
        }
        file_put_contents($path, $content);
        if (!$this->isWindows()) {
            @chmod($path, 0755);
        }

        return [
            'code' => 0,
            'message' => 'success',
            'data' => ['script_path' => $path, 'service_name' => $name],
        ];
    }

    /**
     * 生成 shell 片段：临时解除 /etc/systemd/system 的 immutable 加固。
     *
     * 宝塔等面板的「系统加固」会对 /etc/systemd/system 目录及其中部分 unit 文件执行
     * chattr +i，此时即便 root 写入/删除也会报 Operation not permitted（EPERM），
     * 常规权限判断与 @ 抑制均无效。安装/卸载脚本需先解锁，操作完成后恢复加固。
     *
     * 依赖脚本内已定义的 SYSTEMD_DIR / UNIT_FILE 变量；chattr 或 lsattr 缺失时静默跳过。
     */
    private function systemdLockSnippet(): string
    {
        return "HAS_CHATTR=0\n"
            . "if command -v chattr >/dev/null 2>&1; then HAS_CHATTR=1; fi\n"
            . "DIR_LOCKED=0\n\n"
            . "unlockSystemdDir() {\n"
            . "  if [ \"\$HAS_CHATTR\" -eq 1 ] && command -v lsattr >/dev/null 2>&1 &&\n"
            . "     lsattr -d \"\$SYSTEMD_DIR\" 2>/dev/null | awk '{print \$1}' | grep -q 'i'; then\n"
            . "    echo \"检测到 \$SYSTEMD_DIR 存在 immutable 加固（宝塔系统加固等），临时解除...\"\n"
            . "    if ! chattr -i \"\$SYSTEMD_DIR\"; then\n"
            . "      echo \"解除加固失败，请手工执行：chattr -i \$SYSTEMD_DIR 后重试\"\n"
            . "      exit 1\n"
            . "    fi\n"
            . "    DIR_LOCKED=1\n"
            . "  fi\n"
            . "  # 已存在的 unit 文件可能同样被加固，否则覆盖写入或删除会失败\n"
            . "  if [ \"\$HAS_CHATTR\" -eq 1 ] && [ -f \"\$UNIT_FILE\" ]; then\n"
            . "    chattr -i \"\$UNIT_FILE\" 2>/dev/null || true\n"
            . "  fi\n"
            . "}\n\n"
            . "restoreSystemdDir() {\n"
            . "  if [ \"\$DIR_LOCKED\" -eq 1 ]; then\n"
            . "    chattr +i \"\$SYSTEMD_DIR\" 2>/dev/null || true\n"
            . "    echo \"已恢复 \$SYSTEMD_DIR 的 immutable 加固\"\n"
            . "  fi\n"
            . "}\n\n";
    }

    /**
     * 尝试启动服务。
     * Windows: sc start；Linux: systemctl start。
     * $delayedStartPlanned：延迟启动进程已布置（更新流程经 Go 降级停止的场景），
     * 此时服务将由延迟进程自动拉起，无需也无法再次主动启动，直接返回成功。
     */
    public function start(bool $delayedStartPlanned = false): array
    {
        if ($delayedStartPlanned) {
            return ['code' => 0, 'message' => '延迟启动进程已布置，等待服务自动拉起', 'data' => ['delayed_start' => true]];
        }
        return $this->runServiceCommand('start');
    }

    /**
     * 尝试停止服务。
     * Windows: sc stop；Linux: systemctl stop。
     * $withDelayedStart：更新场景传 true——停止前先布置延迟启动进程（仅经 Go 降级执行时需要），
     * 供服务停止并完成文件替换后自动拉起新版本；仪表盘手动停止不传，服务保持停止状态。
     */
    public function stop(bool $withDelayedStart = false): array
    {
        $res = $this->runServiceCommand('stop');
        if (in_array($res['code'], [403, 503], true)) {
            // PHP 无执行函数或权限不足：降级经本地 Go 服务执行
            // （Go 以系统服务运行，权限更高且不受 PHP disable_functions 限制）
            $goRes = $this->runServiceCommandViaGo('stop', $withDelayedStart);
            if ($goRes['code'] === 0) {
                return $goRes;
            }
            // 降级失败：原 403 错误带命令输出详情（利于排查），优先保留
            return $res['code'] === 403 ? $res : $goRes;
        }
        return $res;
    }

    /**
     * 尝试重启服务。
     * Windows: sc stop + sc start 组合；Linux: systemctl restart。
     */
    public function restart(): array
    {
        $name = $this->serviceName();
        if ($this->isWindows()) {
            // Windows sc 命令没有可靠的 restart 子命令，使用 stop+start 组合
            $stopCmd = 'sc stop ' . escapeshellarg($name) . ' 2>&1';
            $this->safeShellExec($stopCmd);
            // 等待服务完全停止
            sleep(2);
            $startCmd = 'sc start ' . escapeshellarg($name) . ' 2>&1';
            $startRes = $this->safeShellExec($startCmd);
            if ($startRes === null) {
                return [
                    'code' => 503,
                    'message' => 'PHP 环境无法执行服务管理命令（exec/shell_exec 均不可用）。请前往设置页「服务命令」Tab 复制对应命令手动重启服务',
                    'data' => null,
                ];
            }
            // Windows 中文系统下 sc 命令输出为 GBK 编码，转为 UTF-8 避免 JSON 序列化失败
            $output = trim($this->ensureUtf8($startRes['output']));
            $exitCode = (int) $startRes['exit_code'];
            if (str_contains($output, 'Access is denied') || str_contains($output, '拒绝访问')) {
                return [
                    'code' => 403,
                    'message' => '权限不足：请以管理员身份运行',
                    'data' => ['output' => $output],
                ];
            }
            if ($exitCode > 0) {
                return [
                    'code' => 500,
                    'message' => '服务启动命令执行失败（退出码 ' . $exitCode . '）：' . ($output !== '' ? $output : '无输出'),
                    'data' => ['output' => $output],
                ];
            }
            return ['code' => 0, 'message' => '重启命令已发送', 'data' => ['output' => $output]];
        } else {
            $res = $this->runServiceCommand('restart');
            if (in_array($res['code'], [403, 503], true)) {
                // PHP 无法执行（disable_functions）或权限不足：降级经本地 Go 服务执行
                // （Go 以系统服务运行，权限更高且不受 PHP disable_functions 限制）
                return $this->restartViaGo($res);
            }
            return $res;
        }
    }

    /**
     * Linux 重启的降级路径：经本地 Go 服务执行 systemctl restart。
     * Go 服务以系统服务身份运行（root），不受 PHP disable_functions / 运行用户权限限制。
     * Go 自身会随服务重启而退出，HTTP 响应大概率中断（客户端返回 503），属预期现象；
     * systemctl restart 为 systemd restart job，服务必然重新拉起，因此通过轮询
     * status 确认重新上线来判定成功。
     *
     * @param array $phpRes PHP 直接执行失败的原返回（Go 离线无法降级时原样返回）
     * @return array{code:int, message:string, data?:array}
     */
    protected function restartViaGo(array $phpRes): array
    {
        $client = new LocalServiceClient();
        if (!$client->isOnline()) {
            // Go 离线（服务已停止）时无法经 Go 执行任何命令，只能返回原错误引导手动操作
            return $phpRes;
        }

        // 经 Go 执行 systemctl restart：Go 随服务重启退出，响应中断（code != 0）属预期，无需处理返回值
        $client->exec('cmspro.localservice', 'system.exec', [
            'command' => 'systemctl restart ' . escapeshellarg($this->serviceName()),
        ], 10);

        // 轮询确认重新上线：unit RestartSec=5 + Go 启动耗时，约 6-10 秒内恢复；
        // 短超时探测避免个别请求阻塞拖长总时长（8 次 × 2s 间隔，总时长 ~20 秒内）
        for ($i = 0; $i < 8; $i++) {
            sleep(2);
            if ((new LocalServiceClient())->isOnline(2)) {
                return ['code' => 0, 'message' => '重启命令已发送，服务已重新上线', 'data' => ['via' => 'go']];
            }
        }

        return ['code' => 0, 'message' => '重启命令已发送，服务正在重启，请稍后刷新仪表盘确认状态', 'data' => ['via' => 'go']];
    }

    /**
     * 执行服务管理命令（sc/systemctl），捕获无权限 / 执行失败场景。
     * sc 语法: sc [server] command [service name]
     * Windows: sc 命令需要管理员权限，PHP 通常以非管理员身份运行。
     * Linux: 通过 sudo systemctl 执行（需预先配置 sudo NOPASSWD）。
     * 成功判定以退出码为准（exec 路径可靠）；systemctl start/stop/restart
     * 成功时静默无输出，此前按"有输出才算可用"的判断会把静默成功误报为
     * "exec/shell_exec 均不可用"，已修正（空输出 + 退出码非失败 = 成功）。
     */
    protected function runServiceCommand(string $action): array
    {
        $name = $this->serviceName();
        if ($this->isWindows()) {
            $cmd = 'sc ' . $action . ' ' . escapeshellarg($name) . ' 2>&1';
        } else {
            $cmd = '/usr/bin/sudo /usr/bin/systemctl ' . $action . ' ' . escapeshellarg($name) . ' 2>&1';
        }
        $exec = $this->safeShellExec($cmd);

        if ($exec === null) {
            return [
                'code' => 503,
                'message' => 'PHP 环境无法执行服务管理命令（exec/shell_exec 均不可用）。请前往设置页「服务命令」Tab 复制对应命令手动执行，或通过生成的服务脚本操作',
                'data' => null,
            ];
        }

        // Windows 中文系统下 sc 命令输出为 GBK 编码，转为 UTF-8 避免 JSON 序列化失败
        $output = trim($this->ensureUtf8($exec['output']));
        $exitCode = (int) $exec['exit_code'];

        // 失败判定：退出码优先（exec 路径可靠）；退出码未知（shell_exec 路径，-1）时按输出特征判断。
        // 注意：systemctl start/stop/restart 成功时静默无输出，空输出 + 退出码非失败 = 执行成功。
        $denied = $this->isWindows()
            && (str_contains($output, 'Access is denied') || str_contains($output, '拒绝访问'));
        $sudoFailed = !$this->isWindows()
            && (str_contains($output, 'Failed to') || str_contains($output, 'Access denied') || str_contains($output, 'Interactive authentication') || str_contains($output, 'sudoers') || str_contains($output, 'password is required') || str_contains($output, 'terminal is required'));
        $failed = $exitCode > 0 || ($exitCode < 0 && ($denied || $sudoFailed));

        if (!$failed) {
            return ['code' => 0, 'message' => 'success', 'data' => ['output' => $output]];
        }

        // Windows 权限错误
        if ($denied) {
            return [
                'code' => 403,
                'message' => '权限不足：请以管理员身份运行（Windows）或通过生成的服务脚本手动操作（Linux）',
                'data' => ['output' => $output],
            ];
        }

        // Linux 下 sudo 失败（运行用户权限不足）：返回 sudo 授权命令引导配置
        if ($sudoFailed) {
            $actionMap = [
                'start' => '启动',
                'stop' => '停止',
                'restart' => '重启',
            ];
            $actionLabel = $actionMap[$action] ?? $action;
            // 获取当前 PHP 运行用户并生成 sudo 配置命令
            $phpUser = get_current_user();
            $sudoCmd = 'echo "' . $phpUser . ' ALL=(root) NOPASSWD: /usr/bin/systemctl start CmsProLocalService, /usr/bin/systemctl stop CmsProLocalService, /usr/bin/systemctl restart CmsProLocalService, /usr/bin/systemctl status CmsProLocalService" | sudo tee /etc/sudoers.d/cmspro-localservice';
            return [
                'code' => 403,
                'message' => $actionLabel . '失败：PHP 运行用户权限不足。请在服务器上以 root 权限执行以下命令：<br><br><code style="background:#f5f5f5;padding:8px 12px;display:inline-block;border-radius:4px;word-break:break-all;">' . $sudoCmd . '</code>',
                'data' => ['output' => $output],
            ];
        }

        // 其他失败：带退出码与原始输出便于排查
        return [
            'code' => 500,
            'message' => '服务管理命令执行失败（退出码 ' . ($exitCode > 0 ? $exitCode : '未知') . '）：' . ($output !== '' ? $output : '无输出'),
            'data' => ['output' => $output],
        ];
    }

    /**
     * PHP exec/shell_exec 不可用或权限不足时的降级路径：经本地 Go 服务执行服务管理命令。
     * Go 服务以系统服务身份运行（Windows LocalSystem / Linux root），可执行 sc/systemctl。
     *
     * 注意：停止命令执行后 Go 服务进程随之退出，此后无法再经 Go 启动服务。
     * 因此更新场景（$withDelayedStart=true）在停止前先布置「延迟启动」分离进程：
     * Windows 用延迟启动脚本，Linux 用 systemd-run 一次性 timer（10 秒后触发启动），
     * 为文件替换留出时间窗口（更新状态机 verify 轮询约 20 秒可覆盖）。
     *
     * Linux 下停止命令停止的是 Go 服务自身：systemd 向 Go 进程发送 SIGTERM 时
     * HTTP 响应必然中断（如 cURL error 52），与 restartViaGo 同属预期现象，
     * 以「服务已下线」作为停止成功的判定依据（waitServiceOffline），不视为失败。
     */
    protected function runServiceCommandViaGo(string $action, bool $withDelayedStart = false): array
    {
        $client = new LocalServiceClient();
        if (!$client->isOnline()) {
            return [
                'code' => 503,
                'message' => 'PHP 环境无法执行服务管理命令（exec/shell_exec 均不可用），且本地服务离线，无法降级执行。请前往设置页「服务命令」Tab 复制对应命令手动执行',
                'data' => null,
            ];
        }

        // 更新场景：先布置延迟启动进程（Go 停止后无法再经 Go 启动服务）
        $delayedStart = false;
        if ($withDelayedStart) {
            if ($this->isWindows()) {
                $scriptPath = $this->createDelayedStartScript();
                if ($scriptPath === null) {
                    return ['code' => 500, 'message' => '创建延迟启动脚本失败（脚本目录不可写）', 'data' => null];
                }
                $command = 'start "" /min "' . $scriptPath . '"';
            } else {
                // Linux：经 systemd-run 一次性 transient timer 延迟启动。
                // 不用 nohup 后台脚本——该进程归属 Go 服务的 cgroup，systemctl stop
                // （KillMode=control-group 默认）会将其连带 SIGTERM 清理，延迟启动静默失效；
                // systemd-run 的 timer 由 PID 1 管理，不受服务停止影响。
                // AccuracySec=1s 覆盖默认 1min 合并精度，保证 10 秒窗口触发不被拖延
                // （systemd-run 无 --accuracy 选项，须用 --timer-property 指定）。
                $command = 'systemd-run --on-active=10 --timer-property=AccuracySec=1s --collect systemctl start '
                    . escapeshellarg($this->serviceName());
            }
            $launchRes = $client->exec('cmspro.localservice', 'system.exec', ['command' => $command], 15);
            $exitCode = (int) ($launchRes['data']['exit_code'] ?? -1);
            if (($launchRes['code'] ?? -1) !== 0 || $exitCode !== 0) {
                // 命令层面失败（如 systemd-run 参数错误）时以退出码 + 原始输出为准，避免 message（success）掩盖真实原因
                $output = trim((string) ($launchRes['data']['stdout'] ?? ''));
                $reason = ($launchRes['code'] ?? -1) !== 0
                    ? ($launchRes['message'] ?? '未知错误')
                    : ('退出码 ' . $exitCode . ($output !== '' ? '：' . $output : ''));
                return ['code' => 500, 'message' => '布置延迟启动进程失败：' . $reason, 'data' => null];
            }
            $delayedStart = true;
        }

        // 经 Go 执行服务停止命令（Go 返回响应后随服务一起退出）
        if ($this->isWindows()) {
            $execRes = $client->exec('cmspro.localservice', 'service.stop', ['name' => $this->serviceName()], 15);
            $output = (string) ($execRes['data']['output'] ?? '');
        } else {
            $execRes = $client->exec('cmspro.localservice', 'system.exec', [
                'command' => 'systemctl stop ' . escapeshellarg($this->serviceName()),
            ], 15);
            $output = (string) ($execRes['data']['stdout'] ?? '');
        }

        if (($execRes['code'] ?? -1) !== 0) {
            // Linux 下 Go 停止自身时响应必然中断（cURL error 52 等），属预期现象：
            // 轮询确认服务已下线即判定停止成功，仅在下线确认失败时返回错误
            if (!$this->isWindows() && $this->waitServiceOffline()) {
                return [
                    'code' => 0,
                    'message' => 'success',
                    'data' => ['output' => '服务已停止（响应随服务退出中断，属预期）', 'delayed_start' => $delayedStart],
                ];
            }
            return ['code' => 500, 'message' => '经本地服务执行停止命令失败：' . ($execRes['message'] ?? '未知错误'), 'data' => null];
        }

        // Linux 响应正常时检查 systemctl 退出码：明确报错（如 unit 不存在）时提前失败，避免误报成功
        if (!$this->isWindows() && (int) ($execRes['data']['exit_code'] ?? 0) !== 0) {
            $exitCode = (int) ($execRes['data']['exit_code'] ?? 0);
            return [
                'code' => 500,
                'message' => '经本地服务执行停止命令失败（退出码 ' . $exitCode . '）：' . trim($output),
                'data' => null,
            ];
        }

        return [
            'code' => 0,
            'message' => 'success',
            'data' => [
                'output' => $this->ensureUtf8(trim($output)),
                'delayed_start' => $delayedStart,
            ],
        ];
    }

    /**
     * 轮询等待本地服务下线（Linux 经 Go 停止自身场景的成功判定）。
     *
     * 响应中断时 Go 进程已被 systemd SIGTERM 终止，通常首轮短超时探测即确认下线；
     * 轮询总窗口须小于延迟启动脚本的 10 秒等待期，避免把延迟拉起误判为「未停止」。
     */
    protected function waitServiceOffline(): bool
    {
        $client = new LocalServiceClient();
        for ($i = 0; $i < 4; $i++) {
            if (!$client->isOnline(1)) {
                return true;
            }
            if ($i < 3) {
                sleep(1);
            }
        }
        return false;
    }

    /**
     * 生成延迟启动脚本（服务更新流程专用，仅 Windows）：
     * ping -n 11 计时 10 秒后执行启动命令——等待旧进程完全退出与文件替换完成
     * （timeout 命令在无控制台 stdin 时会失败，故用 ping 计时）。
     * Linux 改用 systemd-run 一次性 timer 布置延迟启动，无需脚本文件。
     * 返回脚本绝对路径，创建失败返回 null。
     */
    protected function createDelayedStartScript(): ?string
    {
        $name = $this->serviceName();
        $path = $this->scriptDir() . DIRECTORY_SEPARATOR . 'delayed-start.bat';
        $content = "@echo off\r\n"
            . "rem 服务更新流程：延迟启动本地服务（等待旧进程退出与文件替换完成）\r\n"
            . "ping -n 11 127.0.0.1 >nul\r\n"
            . "sc start \"" . $name . "\"\r\n";
        if (@file_put_contents($path, $content) === false) {
            return null;
        }
        return $path;
    }

    /**
     * 确保字符串为 UTF-8 编码。Windows 中文系统下命令行输出通常为 GBK，
     * 直接放入 JSON 响应会导致 "Malformed UTF-8" 错误。
     */
    private function ensureUtf8(string $output): string
    {
        if ($output === '') {
            return $output;
        }
        $converted = @mb_convert_encoding($output, 'UTF-8', 'GBK,UTF-8');
        return $converted !== false ? $converted : $output;
    }

    /**
     * 服务运行状态（走 HTTP /api/status）。
     *
     * @return array{online:bool, version:?string, binary_exists:bool, script_dir:string}
     */
    public function status(): array
    {
        $online = false;
        $version = null;
        try {
            $res = Http::baseUrl($this->baseUrl())
                ->withToken($this->authToken())
                ->timeout(3)
                ->acceptJson()
                ->get('/api/status')
                ->json();
            if (is_array($res) && ($res['code'] ?? -1) === 0) {
                $online = true;
                $version = $res['data']['version'] ?? null;
            }
        } catch (\Throwable $e) {
            // 服务未运行，保持 $online=false
        }

        return [
            'online' => $online,
            'version' => $version,
            'binary_exists' => is_file($this->binaryPath()),
            'script_dir' => $this->scriptDir(),
        ];
    }

    /**
     * 本地服务基础地址（含端口，如 http://127.0.0.1:9501）。
     *
     * 供其它应用（如 Clineproxy 的 AI 透传扩展）拼装 Go 透传接入地址使用。
     */
    public function relayBaseUrl(): string
    {
        return 'http://127.0.0.1:' . $this->configItem('port', 9501);
    }

    /**
     * 是否运行中（HTTP 探测）。
     */
    public function isRunning(): bool
    {
        try {
            $res = Http::baseUrl($this->baseUrl())
                ->withToken($this->authToken())
                ->timeout(3)
                ->acceptJson()
                ->get('/api/status');
            $json = $res->json();
            return is_array($json) && ($json['code'] ?? -1) === 0;
        } catch (\Throwable $e) {
            return false;
        }
    }

    /**
     * 将 PHP 端配置同步写入 Go 服务配置文件。
     * Go 配置文件路径：storage/local_service_config.json（与 Go config.go 约定一致）。
     */
    public function syncConfig(): string
    {
        $path = storage_path('local_service_config.json');
        $config = [
            'port' => (string) $this->configItem('port', 9501),
            'auth_token' => $this->authToken(),
            'log_level' => (string) $this->configItem('log_level', 'info'),
            'log_retention_days' => (int) $this->configItem('log_retention_days', 30),
            'open_api_enabled' => (bool) $this->configItem('open_api_enabled', 0),
            'open_api_key' => (string) $this->configItem('open_api_key', ''),
            'timezone' => (string) $this->configItem('timezone', 'Asia/Shanghai'),
            // AI 透传扩展回调 CmsPro 的基础地址（Go 决策/日志回调用），数据库未配置时回退 app.url
            'cmspro_base_url' => (string) $this->configItem('cmspro_base_url', config('app.url', 'http://www.cmspro.com')),
        ];

        // MCP 服务配置段（凭据 + 访问控制），由 McpService 组装；装配失败不阻断主配置写入
        try {
            $config['mcp'] = app(McpService::class)->buildGoConfig();
        } catch (\Throwable $e) {
            $config['mcp'] = ['enabled' => false];
        }

        $json = json_encode($config, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES);
        file_put_contents($path, $json);

        // 同步到 Bin/storage/（Go 二进制查找配置的备用路径）
        // 该路径在本地开发时由 Go 服务自动管理，生产环境不一定可写；
        // 同步失败不影响主配置写入，静默跳过即可。
        try {
            $binStoragePath = $this->appPath() . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'storage' . DIRECTORY_SEPARATOR . 'local_service_config.json';
            $binStorageDir = dirname($binStoragePath);
            if (!is_dir($binStorageDir)) {
                @mkdir($binStorageDir, 0755, true);
            }
            if (is_writable($binStorageDir) || !file_exists($binStoragePath)) {
                file_put_contents($binStoragePath, $json);
            }
        } catch (\Throwable $e) {
            // 备用路径不可写是预期情况，不阻塞主流程
        }

        return $path;
    }

    protected function baseUrl(): string
    {
        $port = $this->configItem('port', 9501);
        return 'http://127.0.0.1:' . $port;
    }

    protected function authToken(): string
    {
        return (string) $this->configItem('auth_token', '');
    }

    /**
     * 读取应用配置项。首次访问时一次性加载本应用全部配置到实例缓存，
     * 再按 name 精确取值（避免 LIKE 通配符风险与多次查库）。
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

    /**
     * 当前平台标识（windows / linux / linux-arm64，与版本服务器的平台字段约定一致）。
     */
    protected function platformName(): string
    {
        if ($this->isWindows()) {
            return 'windows';
        }
        return in_array(php_uname('m'), ['aarch64', 'arm64'], true) ? 'linux-arm64' : 'linux';
    }

    /**
     * 请求版本服务器（Versionsvr 应用）最新已发布版本信息。
     *
     * @return array{code:int, message:string, data?:array}
     *         data: {version, changelog, release_date, file_size, checksum, download_url}
     */
    protected function fetchLatestVersion(): array
    {
        try {
            $res = Http::timeout(15)
                ->withOptions(['verify' => false])
                ->acceptJson()
                ->get(UpdateService::SERVER_BASE_URL . '/api/versionsvr/local-service/latest', [
                    'platform' => $this->platformName(),
                ])
                ->json();
        } catch (\Throwable $e) {
            return ['code' => 500, 'message' => '连接版本服务器失败：' . $e->getMessage()];
        }

        if (!is_array($res) || ($res['code'] ?? -1) !== 0) {
            return ['code' => 500, 'message' => '获取最新服务版本失败：' . ($res['message'] ?? '版本服务器返回异常')];
        }

        $data = $res['data'] ?? [];
        if (empty($data['download_url'])) {
            return ['code' => 500, 'message' => '版本服务器未返回下载地址'];
        }
        return ['code' => 0, 'message' => 'success', 'data' => $data];
    }

    /**
     * 本地二进制版本标记（下载成功时写入 Bin/local-service.version）。
     * 标记不存在视为未知版本（存量旧文件），生成安装脚本时会触发重新下载。
     */
    protected function localBinaryVersion(): ?string
    {
        $marker = $this->appPath() . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'local-service.version';
        if (!is_file($marker)) {
            return null;
        }
        $version = trim((string) file_get_contents($marker));
        return $version !== '' ? $version : null;
    }

    /**
     * 写入本地二进制版本标记。
     */
    protected function writeBinaryVersion(string $version): void
    {
        if ($version === '') {
            return;
        }
        @file_put_contents(
            $this->appPath() . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'local-service.version',
            $version
        );
    }

    /**
     * 确保本地二进制为版本服务器最新已发布版本（生成安装脚本前调用）。
     * 本地无二进制时必须下载成功；已有二进制时与版本标记比对，落后或未知则重新下载，
     * 一致则跳过；版本服务器不可达或下载失败但有本地文件时降级使用本地文件，不阻塞脚本生成。
     *
     * @param string $binary 当前平台实际使用的二进制路径
     * @return array{code:int, message:string, data?:array}
     */
    protected function ensureBinaryUpToDate(string $binary): array
    {
        $localExists = is_file($binary);

        $latestRes = $this->fetchLatestVersion();
        if ($latestRes['code'] !== 0) {
            if ($localExists) {
                return ['code' => 0, 'message' => '版本服务器不可用，使用本地已有二进制'];
            }
            return $latestRes;
        }

        $latest = $latestRes['data'];
        if ($localExists && $this->localBinaryVersion() === (string) $latest['version']) {
            return ['code' => 0, 'message' => '本地二进制已是最新版本 v' . $latest['version']];
        }

        $downloadResult = $this->downloadBinary($latest);
        if ($downloadResult['code'] !== 0 && $localExists) {
            return ['code' => 0, 'message' => '下载最新版本失败，使用本地已有二进制'];
        }
        return $downloadResult;
    }

    /**
     * 服务管理命令清单（含环境检测信息），供设置页「服务命令」Tab 展示。
     *
     * 背景：宝塔等面板常禁用 exec/shell_exec（或运行用户无权限），此时后台启停按钮
     * 无法直接执行 sc/systemctl，需用户在服务器上以管理员/root 身份手动执行命令。
     * 命令内容与 runServiceCommand()/sudoers 降级提示保持一致。
     *
     * @return array{platform:string, php_user:string, exec_disabled:bool, service_name:string, script_dir:string, groups:array}
     */
    public function serviceCommands(): array
    {
        $name = $this->serviceName();

        if ($this->isWindows()) {
            $groups = [
                [
                    'title' => '服务管理命令（以管理员身份运行 CMD 执行）',
                    'items' => [
                        ['label' => '启动服务', 'command' => 'sc start "' . $name . '"'],
                        ['label' => '停止服务', 'command' => 'sc stop "' . $name . '"'],
                        ['label' => '重启服务', 'command' => 'sc stop "' . $name . '" & timeout /t 3 /nobreak >nul & sc start "' . $name . '"'],
                        ['label' => '查询状态', 'command' => 'sc query "' . $name . '"'],
                    ],
                ],
            ];
        } else {
            $phpUser = get_current_user();
            $sudoers = $phpUser . ' ALL=(root) NOPASSWD: /usr/bin/systemctl start ' . $name
                . ', /usr/bin/systemctl stop ' . $name
                . ', /usr/bin/systemctl restart ' . $name
                . ', /usr/bin/systemctl status ' . $name;
            $groups = [
                [
                    'title' => '服务管理命令（以 root 权限执行）',
                    'items' => [
                        ['label' => '启动服务', 'command' => 'sudo systemctl start ' . $name],
                        ['label' => '停止服务', 'command' => 'sudo systemctl stop ' . $name],
                        ['label' => '重启服务', 'command' => 'sudo systemctl restart ' . $name],
                        ['label' => '查询状态', 'command' => 'sudo systemctl status ' . $name . ' --no-pager'],
                    ],
                ],
                [
                    'title' => 'Web 按钮授权（一次性配置，配好后可直接在后台点按钮管理服务）',
                    'items' => [
                        ['label' => '配置 sudo 免密授权', 'command' => 'echo "' . $sudoers . '" | sudo tee /etc/sudoers.d/cmspro-localservice'],
                    ],
                ],
            ];
        }

        return [
            'platform' => $this->isWindows() ? 'windows' : 'linux',
            'php_user' => get_current_user(),
            'exec_disabled' => !function_exists('shell_exec') && !function_exists('exec'),
            'service_name' => $name,
            'script_dir' => $this->scriptDir(),
            'groups' => $groups,
        ];
    }

    /**
     * 从版本服务器下载当前系统对应的 Go 服务二进制文件（最新已发布版本）。
     * 下载地址与校验值（大小 / SHA256）来自版本服务器 latest 接口，
     * 下载后存储到应用 Bin 目录并写入版本标记。
     *
     * @param array|null $latest 已获取的最新版本信息（fetchLatestVersion 的 data），
     *                           为空时现场请求，供卸载脚本等场景复用。
     * @return array{code:int, message:string, data?:array}
     */
    protected function downloadBinary(?array $latest = null): array
    {
        $appPath = $this->appPath();

        if ($this->isWindows()) {
            $saveDir = $appPath . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'windows';
            $savePath = $saveDir . DIRECTORY_SEPARATOR . 'cmspro-local-service.exe';
        } else {
            // Linux 下自动检测架构（php_uname 为纯 PHP 函数，不受 disable_functions 影响）
            if (in_array(php_uname('m'), ['aarch64', 'arm64'], true)) {
                $savePath = $appPath . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'linux' . DIRECTORY_SEPARATOR . 'cmspro-local-service-arm64';
            } else {
                $savePath = $appPath . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'linux' . DIRECTORY_SEPARATOR . 'cmspro-local-service';
            }
            $saveDir = dirname($savePath);
        }

        // 确保目标目录存在
        if (!is_dir($saveDir)) {
            @mkdir($saveDir, 0755, true);
        }

        // 检查目录是否可写
        if (!is_writable($saveDir)) {
            return ['code' => 500, 'message' => '目录不可写，无法下载二进制文件: ' . $saveDir];
        }

        // 版本信息未预取时现场获取
        if ($latest === null) {
            $latestRes = $this->fetchLatestVersion();
            if ($latestRes['code'] !== 0) {
                return $latestRes;
            }
            $latest = $latestRes['data'];
        }

        try {
            $response = Http::timeout(60)
                ->withOptions(['verify' => false])
                ->get(UpdateService::SERVER_BASE_URL . $latest['download_url']);

            if (!$response->successful()) {
                return ['code' => 500, 'message' => '下载二进制文件失败，HTTP 状态码: ' . $response->status()];
            }

            $content = $response->body();
            if (empty($content)) {
                return ['code' => 500, 'message' => '下载内容为空'];
            }

            // 完整性校验：大小 / SHA256（与版本服务器登记信息比对）
            $expectedSize = (int) ($latest['file_size'] ?? 0);
            if ($expectedSize > 0 && strlen($content) !== $expectedSize) {
                return ['code' => 500, 'message' => '文件大小校验失败（期望 ' . $expectedSize . ' 字节，实际 ' . strlen($content) . ' 字节）'];
            }
            $checksum = (string) ($latest['checksum'] ?? '');
            if ($checksum !== '' && hash('sha256', $content) !== $checksum) {
                return ['code' => 500, 'message' => '文件 SHA256 校验失败，下载内容可能已损坏'];
            }

            file_put_contents($savePath, $content);

            if (!is_file($savePath) || filesize($savePath) === 0) {
                return ['code' => 500, 'message' => '保存二进制文件失败'];
            }

            // Linux 下设置执行权限
            if (!$this->isWindows()) {
                @chmod($savePath, 0755);
            }

            // 记录版本标记，供生成安装脚本时与版本服务器比对
            $this->writeBinaryVersion((string) ($latest['version'] ?? ''));

            return ['code' => 0, 'message' => '已下载最新服务版本 v' . ($latest['version'] ?? '?'), 'data' => ['binary_path' => $savePath]];
        } catch (\Throwable $e) {
            return ['code' => 500, 'message' => '下载二进制文件异常: ' . $e->getMessage()];
        }
    }
}