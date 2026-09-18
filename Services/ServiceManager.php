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
     * 因此先经 function_exists 检测；两个函数均不可用时返回 null，由调用方走兜底提示。
     */
    protected function safeShellExec(string $cmd): ?string
    {
        if (function_exists('shell_exec')) {
            $output = @shell_exec($cmd);
            if ($output !== null && $output !== false && trim($output) !== '') {
                return $output;
            }
        }
        if (function_exists('exec')) {
            $lines = [];
            $exitCode = -1;
            @exec($cmd, $lines, $exitCode);
            if (!empty($lines)) {
                return implode("\n", $lines);
            }
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
     * 二进制不存在时自动从远程下载。
     *
     * @return array{code:int, message:string, data?:array}
     */
    public function installScript(): array
    {
        $binary = $this->binaryPath();
        if (!is_file($binary)) {
            $downloadResult = $this->downloadBinary();
            if ($downloadResult['code'] !== 0) {
                return $downloadResult;
            }
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
     */
    public function start(): array
    {
        return $this->runServiceCommand('start');
    }

    /**
     * 尝试停止服务。
     * Windows: sc stop；Linux: systemctl stop。
     */
    public function stop(): array
    {
        return $this->runServiceCommand('stop');
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
            $output = $this->safeShellExec($startCmd);
            if ($output === null) {
                return [
                    'code' => 503,
                    'message' => 'PHP 环境无法执行服务管理命令，请手动重启服务',
                    'data' => null,
                ];
            }
            // Windows 中文系统下 sc 命令输出为 GBK 编码，转为 UTF-8 避免 JSON 序列化失败
            $output = $this->ensureUtf8($output);
            $output = trim($output);
            if (str_contains($output, 'Access is denied') || str_contains($output, '拒绝访问')) {
                return [
                    'code' => 403,
                    'message' => '权限不足：请以管理员身份运行',
                    'data' => ['output' => $output],
                ];
            }
            return ['code' => 0, 'message' => '重启命令已发送', 'data' => ['output' => $output]];
        } else {
            return $this->runServiceCommand('restart');
        }
    }

    /**
     * 执行服务管理命令（sc/systemctl），捕获无权限 / 执行失败场景。
     * sc 语法: sc [server] command [service name]
     * Windows: sc 命令需要管理员权限，PHP 通常以非管理员身份运行。
     * Linux: 通过 sudo systemctl 执行（需预先配置 sudo NOPASSWD），
     * 如果 sudo 也不可用，由后续逻辑返回明确错误。
     */
    protected function runServiceCommand(string $action): array
    {
        $name = $this->serviceName();
        if ($this->isWindows()) {
            $cmd = 'sc ' . $action . ' ' . escapeshellarg($name) . ' 2>&1';
        } else {
            $cmd = '/usr/bin/sudo /usr/bin/systemctl ' . $action . ' ' . escapeshellarg($name) . ' 2>&1';
        }
        $output = $this->safeShellExec($cmd);

        if ($output === null) {
            return [
                'code' => 503,
                'message' => 'PHP 环境无法执行服务管理命令（exec/shell_exec 均不可用），请通过生成的服务脚本手动操作',
                'data' => null,
            ];
        }

        // Windows 中文系统下 sc 命令输出为 GBK 编码，转为 UTF-8 避免 JSON 序列化失败
        $output = $this->ensureUtf8($output);
        $output = trim($output);

        // 检测常见权限错误
        if (str_contains($output, 'Access is denied') || str_contains($output, '拒绝访问')) {
            return [
                'code' => 403,
                'message' => '权限不足：请以管理员身份运行（Windows）或通过生成的服务脚本手动操作（Linux）',
                'data' => ['output' => $output],
            ];
        }

        // Linux 下 sudo 失败特征（非 Linux 环境跳过此检测）
        if (!$this->isWindows() && (str_contains($output, 'Failed to') || str_contains($output, 'Access denied') || str_contains($output, 'Interactive authentication') || str_contains($output, 'sudoers') || str_contains($output, 'password is required') || str_contains($output, 'terminal is required'))) {
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

        return ['code' => 0, 'message' => 'success', 'data' => ['output' => $output]];
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
     * 从远程下载当前系统对应的 Go 服务二进制文件。
     * 下载后存储到应用 Bin 目录中。
     *
     * @return array{code:int, message:string, data?:array}
     */
    protected function downloadBinary(): array
    {
        $appPath = $this->appPath();

        if ($this->isWindows()) {
            $url = 'https://v5.cmspro.cn/apps/localservice/windows/cmspro-local-service.exe';
            $saveDir = $appPath . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'windows';
            $savePath = $saveDir . DIRECTORY_SEPARATOR . 'cmspro-local-service.exe';
        } else {
            // Linux 下自动检测架构（php_uname 为纯 PHP 函数，不受 disable_functions 影响）
            $arch = php_uname('m');
            if (in_array($arch, ['aarch64', 'arm64'])) {
                $url = 'https://v5.cmspro.cn/apps/localservice/linux/cmspro-local-service-arm64';
                $savePath = $appPath . DIRECTORY_SEPARATOR . 'Bin' . DIRECTORY_SEPARATOR . 'linux' . DIRECTORY_SEPARATOR . 'cmspro-local-service-arm64';
            } else {
                $url = 'https://v5.cmspro.cn/apps/localservice/linux/cmspro-local-service';
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

        try {
            $response = Http::timeout(60)
                ->withOptions(['verify' => false])
                ->get($url);

            if (!$response->successful()) {
                return ['code' => 500, 'message' => '下载二进制文件失败，HTTP 状态码: ' . $response->status()];
            }

            $content = $response->body();
            if (empty($content)) {
                return ['code' => 500, 'message' => '下载内容为空'];
            }

            file_put_contents($savePath, $content);

            if (!is_file($savePath) || filesize($savePath) === 0) {
                return ['code' => 500, 'message' => '保存二进制文件失败'];
            }

            // Linux 下设置执行权限
            if (!$this->isWindows()) {
                @chmod($savePath, 0755);
            }

            return ['code' => 0, 'message' => '二进制文件下载成功', 'data' => ['binary_path' => $savePath]];
        } catch (\Throwable $e) {
            return ['code' => 500, 'message' => '下载二进制文件异常: ' . $e->getMessage()];
        }
    }
}