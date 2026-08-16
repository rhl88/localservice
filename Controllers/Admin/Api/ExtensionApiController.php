<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin\Api;

use App\Apps\CmsproLocalservice\Services\LocalServiceClient;
use App\Http\Responses\ApiResponse;
use Illuminate\Http\Request;

class ExtensionApiController
{
    private const CHROME_PATHS = [
        'C:\Program Files\Google\Chrome\Application\chrome.exe',
        'C:\Program Files (x86)\Google\Chrome\Application\chrome.exe',
    ];

    public function __construct(private LocalServiceClient $client) {}

    /**
     * 拓展列表。
     */
    public function index()
    {
        $res = $this->client->listExtensions();
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '获取拓展失败');
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 拓展详情。
     */
    public function show($name)
    {
        $res = $this->client->getExtension($name);
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '获取拓展详情失败');
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 更新拓展配置。
     */
    public function update(Request $request, $name)
    {
        $data = $request->only(['path', 'config', 'status']);
        if (isset($data['status'])) {
            $data['status'] = (int) $data['status'];
        }
        $res = $this->client->updateExtension($name, $data);
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '更新拓展失败');
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 测试拓展可用性。
     */
    public function test($name)
    {
        $res = $this->client->testExtension($name);
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '拓展测试失败');
        }
        return ApiResponse::success($res['data']);
    }

    /**
     * 安装拓展（自动检测路径并启用）。
     * 支持 browser、script 等类型自动检测，未检测到时返回可手动配置的提示。
     */
    public function install(Request $request, $name)
    {
        // 支持前端传入手动填写的路径
        $manualPath = $request->input('path', '');

        // 通过 Go 端获取当前拓展信息
        $res = $this->client->getExtension($name);
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error(404, '拓展不存在: ' . $name);
        }
        $ext = $res['data'];

        $type = $ext['type'] ?? '';
        $detectedPath = '';

        if ($manualPath) {
            // 用户手动填写了路径
            if (!is_file($manualPath)) {
                return ApiResponse::error(404, '指定路径不存在，请确认路径正确');
            }
            $detectedPath = $manualPath;
        } else {
            // 自动检测
            $detectedPath = $this->autoDetectPath($name, $type);
        }

        if (!$detectedPath) {
            return ApiResponse::error(404, '未检测到 ' . ($ext['title'] ?? $name) . '，请在弹窗中手动填写安装路径');
        }

        // 通过 Go 端 update 写入路径并启用
        $updateRes = $this->client->updateExtension($name, [
            'path' => $detectedPath,
            'status' => 1,
        ]);
        if (($updateRes['code'] ?? -1) !== 0) {
            return ApiResponse::error($updateRes['code'] ?? 500, $updateRes['message'] ?? '安装失败');
        }

        return ApiResponse::success([
            'name' => $name,
            'path' => $detectedPath,
            'status' => 1,
            'title' => $ext['title'] ?? $name,
        ]);
    }

    /**
     * 通过 Go 后台异步执行指令来安装拓展（如 Python、ffmpeg 等）。
     * 异步执行不阻塞 PHP 请求，返回 task_id 供前端轮询结果。
     */
    public function installByCommand(Request $request, $name)
    {
        $command = $request->input('command', '');
        if (!$command) {
            return ApiResponse::error(400, '请填写安装指令');
        }

        // 通过 Go 端获取当前拓展信息，验证拓展存在
        $res = $this->client->getExtension($name);
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error(404, '拓展不存在: ' . $name);
        }

        // 异步执行（不阻塞 PHP 请求）
        $taskId = $this->client->execAsync('cmspro_localservice', 'system.exec', [
            'command' => $command,
            'timeout' => 300,
        ]);

        if (!$taskId) {
            return ApiResponse::error(500, '提交安装任务失败');
        }

        return ApiResponse::success([
            'name' => $name,
            'task_id' => $taskId,
        ]);
    }

    /**
     * 获取系统推荐安装指令。
     * 根据当前操作系统和拓展名返回默认的安装命令。
     */
    public function getDefaultInstallCommand($name)
    {
        $isWindows = strtoupper(substr(PHP_OS, 0, 3)) === 'WIN';

        $commands = [];

        if ($name === 'python') {
            if ($isWindows) {
                $commands[] = [
                    'title' => 'winget 安装 Python 3.12',
                    'command' => 'winget install Python.Python.3.12 --accept-package-agreements --accept-source-agreements',
                    'description' => '通过 Windows 包管理器 winget 安装（Windows 11 / Win10 1809+ 内置）',
                ];
                $commands[] = [
                    'title' => 'winget 安装 Python 3.11',
                    'command' => 'winget install Python.Python.3.11 --accept-package-agreements --accept-source-agreements',
                    'description' => 'Python 3.11 版本（兼容性更稳定）',
                ];
                $commands[] = [
                    'title' => 'choco 安装 Python',
                    'command' => 'choco install python -y',
                    'description' => '通过 Chocolatey 包管理器安装（需预先安装 choco）',
                ];
            } else {
                $commands[] = [
                    'title' => 'apt 安装 Python3',
                    'command' => 'apt-get update && apt-get install -y python3 python3-pip',
                    'description' => 'Debian/Ubuntu 系 Linux',
                ];
                $commands[] = [
                    'title' => 'yum 安装 Python3',
                    'command' => 'yum install -y python3 python3-pip',
                    'description' => 'CentOS/RHEL 系 Linux',
                ];
                $commands[] = [
                    'title' => 'brew 安装 Python3',
                    'command' => 'brew install python@3.12',
                    'description' => 'macOS Homebrew',
                ];
            }
        } elseif ($name === 'chrome') {
            if ($isWindows) {
                $commands[] = [
                    'title' => '下载安装 Chrome',
                    'command' => 'start https://www.google.com/chrome/',
                    'description' => 'Windows 下 Chrome 推荐手动下载安装',
                ];
            } else {
                $commands[] = [
                    'title' => 'apt 安装 Chromium',
                    'command' => 'apt-get update && apt-get install -y chromium-browser',
                    'description' => 'Debian/Ubuntu 系 Linux',
                ];
                $commands[] = [
                    'title' => 'yum 安装 Chromium',
                    'command' => 'yum install -y chromium',
                    'description' => 'CentOS/RHEL 系 Linux',
                ];
                $commands[] = [
                    'title' => 'snap 安装 Chromium',
                    'command' => 'snap install chromium',
                    'description' => '通过 snap 安装（推荐 Ubuntu）',
                ];
                $commands[] = [
                    'title' => '下载安装 Chrome',
                    'command' => 'wget -q -O /tmp/chrome.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb && apt-get update && dpkg -i /tmp/chrome.deb || apt-get install -f -y',
                    'description' => '通过 deb 包安装 Google Chrome',
                ];
            }
        } elseif ($name === 'ffmpeg') {
            if ($isWindows) {
                $commands[] = [
                    'title' => 'winget 安装 FFmpeg',
                    'command' => 'winget install FFmpeg --accept-package-agreements --accept-source-agreements',
                    'description' => '通过 winget 安装',
                ];
                $commands[] = [
                    'title' => 'choco 安装 FFmpeg',
                    'command' => 'choco install ffmpeg -y',
                    'description' => '通过 Chocolatey 安装',
                ];
            } else {
                $commands[] = [
                    'title' => 'apt 安装 FFmpeg',
                    'command' => 'apt-get update && apt-get install -y ffmpeg',
                    'description' => 'Debian/Ubuntu 系 Linux',
                ];
                $commands[] = [
                    'title' => 'brew 安装 FFmpeg',
                    'command' => 'brew install ffmpeg',
                    'description' => 'macOS Homebrew',
                ];
            }
        }

        return ApiResponse::success([
            'name' => $name,
            'is_windows' => $isWindows,
            'commands' => $commands,
        ]);
    }

    /**
     * 查询安装任务执行结果，并尝试自动检测已安装的路径。
     */
    public function getInstallTaskResult($name, $taskId)
    {
        // 查询任务状态
        $res = $this->client->getTaskStatus($taskId);
        if (($res['code'] ?? -1) !== 0) {
            return ApiResponse::error($res['code'] ?? 500, $res['message'] ?? '查询任务状态失败');
        }

        $task = $res['data'] ?? [];
        $status = $task['status'] ?? 'unknown';

        $result = [
            'name' => $name,
            'task_id' => $taskId,
            'status' => $status,
            'exit_code' => null,
            'stdout' => null,
            'detected_path' => '',
        ];

        if (in_array($status, ['success', 'failed'], true)) {
            // 任务已完成，尝试解析结果
            $taskResult = $task['result'] ?? $task;
            if (is_string($taskResult)) {
                $parsed = json_decode($taskResult, true);
                if (is_array($parsed)) {
                    $result['exit_code'] = $parsed['exit_code'] ?? null;
                    $result['stdout'] = $parsed['stdout'] ?? null;
                }
            } elseif (is_array($taskResult)) {
                $result['exit_code'] = $taskResult['exit_code'] ?? null;
                $result['stdout'] = $taskResult['stdout'] ?? null;
            }

            // 尝试自动检测已安装的路径
            if ($name === 'python') {
                $detected = $this->findPythonFromRegistry();
                if ($detected) {
                    $result['detected_path'] = $detected;
                    // 自动更新路径并启用
                    $this->client->updateExtension($name, [
                        'path' => $detected,
                        'status' => 1,
                    ]);
                }
            }
        }

        return ApiResponse::success($result);
    }

    /**
     * 根据类型自动检测路径。
     * 注意：对于 python 等已知拓展，即使 type 不匹配也强制检测，
     * 兼容 Go 端未正确设置 type 的场景。
     */
    private function autoDetectPath(string $name, string $type): string
    {
        if ($type === 'browser') {
            return $this->detectBrowserPath($name);
        }
        if ($type === 'script' || $type === 'media') {
            return $this->detectScriptPath($name);
        }
        // 已知的可执行程序兜底检测
        $known = ['python', 'ffmpeg'];
        if (in_array($name, $known, true)) {
            return $this->detectScriptPath($name);
        }
        return '';
    }

    /**
     * 检测浏览器路径，支持 Chrome/Edge。
     * 兼容 Linux、macOS 和 Windows。
     */
    private function detectBrowserPath(string $name): string
    {
        $searchPaths = [];
        $isWindows = strtoupper(substr(PHP_OS, 0, 3)) === 'WIN';

        if ($name === 'chrome') {
            if ($isWindows) {
                $searchPaths = self::CHROME_PATHS;
                $localAppData = getenv('LOCALAPPDATA');
                if ($localAppData) {
                    $searchPaths[] = $localAppData . '\Google\Chrome\Application\chrome.exe';
                }
                // Edge 兜底
                $searchPaths[] = 'C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe';
                $searchPaths[] = 'C:\Program Files\Microsoft\Edge\Application\msedge.exe';
            } else {
                // Linux / macOS 常见路径
                $linuxPaths = [
                    '/usr/bin/google-chrome',
                    '/usr/bin/google-chrome-stable',
                    '/usr/bin/chromium',
                    '/usr/bin/chromium-browser',
                    '/usr/lib/chromium/chromium',
                    '/snap/bin/chromium',
                    '/usr/bin/google-chrome-beta',
                    '/usr/bin/google-chrome-unstable',
                ];
                $macPaths = [
                    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
                    '/Applications/Chromium.app/Contents/MacOS/Chromium',
                ];
                $searchPaths = strtoupper(PHP_OS) === 'DARWIN' ? $macPaths : $linuxPaths;
            }
        }

        foreach ($searchPaths as $p) {
            if (is_file($p)) {
                return $p;
            }
        }

        // 如果找不到文件路径，通过 shell 命令查找（where/which google-chrome）
        if (!$isWindows) {
            foreach (['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser'] as $cmd) {
                $output = @shell_exec('which ' . escapeshellarg($cmd) . ' 2>/dev/null');
                if ($output) {
                    $line = trim($output);
                    if ($line && is_file($line)) {
                        return $line;
                    }
                }
            }
        }

        return '';
    }

    /**
     * 检测脚本/媒体类程序路径（通过 PATH 环境变量查找 + where/which 命令兜底 + 执行命令版本检测）。
     */
    private function detectScriptPath(string $name): string
    {
        $execName = $this->getExecName($name);
        if (!$execName) {
            return '';
        }

        // 针对 Python 特殊处理：通过执行 --version 检测是否可用
        if ($name === 'python') {
            return $this->detectPythonPath();
        }

        // 方式一：遍历 PATH 目录查找
        $pathEnv = getenv('PATH');
        if ($pathEnv) {
            $dirs = explode(PATH_SEPARATOR, $pathEnv);
            foreach ($dirs as $dir) {
                // 解析环境变量占位符（如 %USERPROFILE%）
                $dir = preg_replace_callback('/%([^%]+)%/', function ($m) {
                    return getenv($m[1]) ?: $m[0];
                }, $dir);

                $fullPath = $dir . DIRECTORY_SEPARATOR . $execName;
                if (is_file($fullPath)) {
                    return $fullPath;
                }
            }
        }

        // 方式二：通过 where/which 命令查找
        $isWindows = strtoupper(substr(PHP_OS, 0, 3)) === 'WIN';
        if ($isWindows) {
            $output = @shell_exec('where ' . escapeshellarg($execName) . ' 2>nul');
        } else {
            $output = @shell_exec('which ' . escapeshellarg($execName) . ' 2>/dev/null');
        }
        if ($output) {
            $lines = explode("\n", trim($output));
            foreach ($lines as $line) {
                $line = trim($line);
                if ($line && is_file($line)) {
                    return $line;
                }
            }
        }

        return '';
    }

    /**
     * 检测 Python 安装路径。
     * 优先从 Windows 注册表读取安装路径（Web SAPI 下 PATH 环境变量可能不包含 Python），
     * 然后依次尝试 python --version、py --version、python3 --version，
     * 只要能输出版本号就说明已安装，并通过 where/which 定位可执行文件路径。
     */
    private function detectPythonPath(): string
    {
        $isWindows = strtoupper(substr(PHP_OS, 0, 3)) === 'WIN';

        // Windows 下优先从注册表读取 Python 安装路径
        if ($isWindows) {
            $regPath = $this->findPythonFromRegistry();
            if ($regPath !== '') {
                return $regPath;
            }
        }

        // 待尝试的命令列表
        $candidates = $isWindows ? ['python', 'py', 'python3'] : ['python3', 'python'];

        foreach ($candidates as $cmd) {
            // 方式一：通过 where/which 定位完整路径
            $foundPath = $this->locateExecutable($cmd);
            if ($foundPath !== '') {
                return $foundPath;
            }

            // 方式二：直接执行 --version 检测（处理 App Execution Aliases 等场景）
            if ($this->checkVersion($cmd)) {
                // 能执行但 locate 找不到完整路径，再尝试一次 locate（部分场景第二次能成功）
                $foundPath = $this->locateExecutable($cmd);
                if ($foundPath !== '') {
                    return $foundPath;
                }
                // 确实找不到文件路径，返回命令名让上层通过 shell 执行
                return $cmd;
            }
        }

        return '';
    }

    /**
     * 从 Windows 注册表 + 常见安装目录查找 Python 安装路径。
     * Web SAPI（如 nginx + PHP-FPM）下 PATH 环境变量可能与命令行不同，
     * 因此综合使用注册表查询 + 常见路径遍历两种方式。
     */
    private function findPythonFromRegistry(): string
    {
        $paths = [];

        // 方式一：遍历常见的 Python 安装目录（不依赖任何外部命令，最可靠）
        $commonDirs = $this->getCommonPythonDirs();
        foreach ($commonDirs as $dir) {
            $exePath = $dir . '\\python.exe';
            if (is_file($exePath)) {
                $paths[] = $exePath;
            }
        }

        // 方式二：通过 reg query 命令读取注册表（补充检测）
        $baseKeys = [
            'HKCU\SOFTWARE\Python\PythonCore',
            'HKLM\SOFTWARE\Python\PythonCore',
            'HKCU\SOFTWARE\WOW6432Node\Python\PythonCore',
            'HKLM\SOFTWARE\WOW6432Node\Python\PythonCore',
        ];
        foreach ($baseKeys as $baseKey) {
            $output = @shell_exec('reg query "' . $baseKey . '" 2>nul');
            if (!$output) {
                continue;
            }
            $lines = explode("\n", $output);
            foreach ($lines as $line) {
                $line = trim($line);
                if ($line === '' || stripos($line, 'HKEY_') !== 0) {
                    continue;
                }
                $installKey = $line . '\InstallPath';
                $installOut = @shell_exec('reg query "' . $installKey . '" 2>nul');
                if (!$installOut) {
                    continue;
                }
                // 取 ExecutablePath 值
                $val = $this->parseRegValue($installOut, 'ExecutablePath');
                if ($val !== '' && is_file($val)) {
                    $paths[] = $val;
                }
                // 取 (Default) 值
                $val = $this->parseRegDefault($installOut);
                if ($val !== '') {
                    $val = rtrim($val, '\\/') . '\\python.exe';
                    if (is_file($val)) {
                        $paths[] = $val;
                    }
                }
            }
        }

        // 去重返回第一个有效路径
        $seen = [];
        foreach ($paths as $p) {
            $normalized = strtolower(str_replace(['/', '\\'], '\\', $p));
            if (in_array($normalized, $seen, true)) {
                continue;
            }
            $seen[] = $normalized;
            if (is_file($p)) {
                return $p;
            }
        }
        return '';
    }

    /**
     * 获取常见的 Python 安装目录列表，不依赖任何外部命令。
     * 生成所有可能的路径组合，由调用方通过 is_file 验证。
     */
    private function getCommonPythonDirs(): array
    {
        $dirs = [];
        $envVars = [];

        // 收集所有能获取到的环境变量
        foreach (['ProgramFiles', 'ProgramFiles(x86)', 'LOCALAPPDATA', 'USERPROFILE', 'SystemDrive', 'HOMEDRIVE', 'HOMEPATH'] as $name) {
            $val = getenv($name);
            if ($val) {
                $envVars[$name] = $val;
            }
        }

        // 兜底：常见系统路径（环境变量拿不到时用）
        $envVars['_fallback_ProgramFiles'] = 'C:\Program Files';
        $envVars['_fallback_ProgramFilesX86'] = 'C:\Program Files (x86)';
        $envVars['_fallback_SystemDrive'] = 'C:';
        // USERPROFILE 兜底：从 HOMEDRIVE+HOMEPATH 拼
        if (!isset($envVars['USERPROFILE']) && isset($envVars['HOMEDRIVE']) && isset($envVars['HOMEPATH'])) {
            $envVars['USERPROFILE'] = $envVars['HOMEDRIVE'] . $envVars['HOMEPATH'];
        }
        $envVars['_fallback_UserProfile'] = 'C:\Users\Administrator';
        // LOCALAPPDATA 兜底
        if (!isset($envVars['LOCALAPPDATA']) && isset($envVars['USERPROFILE'])) {
            $envVars['LOCALAPPDATA'] = $envVars['USERPROFILE'] . '\AppData\Local';
        } elseif (!isset($envVars['LOCALAPPDATA'])) {
            $envVars['LOCALAPPDATA'] = $envVars['_fallback_UserProfile'] . '\AppData\Local';
        }

        $pf = $envVars['ProgramFiles'] ?? $envVars['_fallback_ProgramFiles'];
        $pfx86 = $envVars['ProgramFiles(x86)'] ?? $envVars['_fallback_ProgramFilesX86'];
        $localAppData = $envVars['LOCALAPPDATA'];
        $userProfile = $envVars['USERPROFILE'] ?? $envVars['_fallback_UserProfile'];
        $sd = $envVars['SystemDrive'] ?? $envVars['_fallback_SystemDrive'];

        // 版本列表
        $versions = ['313', '312', '311', '310', '39', '38', '37'];

        // Program Files
        $baseDirs = [
            $pf . '\Python',
        ];
        // Program Files (x86)
        if ($pfx86 !== $pf) {
            $baseDirs[] = $pfx86 . '\Python';
        }
        // LocalAppData
        $baseDirs[] = $localAppData . '\Programs\Python';
        // UserProfile
        $baseDirs[] = $userProfile . '\AppData\Local\Programs\Python';
        // SystemDrive
        $baseDirs[] = $sd;

        // 微软商店 WindowsApps
        $baseDirs[] = $localAppData . '\Microsoft\WindowsApps';
        $baseDirs[] = $userProfile . '\AppData\Local\Microsoft\WindowsApps';

        foreach ($baseDirs as $base) {
            // 对每个基础目录，生成带版本后缀的路径
            foreach ($versions as $ver) {
                $dirs[] = $base . '\Python' . $ver;
            }
            // 也加入基础目录本身（如 C:\Python 或直接安装到目录根的情况）
            if (strpos($base, 'Python') !== false || $base === $sd) {
                $dirs[] = $base;
            }
        }

        return $dirs;
    }

    /**
     * 从 reg query 输出中解析指定值的数据。
     * 匹配格式: "    ExecutablePath    REG_SZ    C:\Python312\python.exe"
     */
    private function parseRegValue(string $output, string $valueName): string
    {
        foreach (explode("\n", $output) as $line) {
            $line = trim($line);
            if (preg_match('/^' . preg_quote($valueName, '/') . '\s+REG_\w+\s+(.+)$/i', $line, $m)) {
                return trim($m[1]);
            }
        }
        return '';
    }

    /**
     * 从 reg query 输出中解析 (Default) 值。
     * 匹配格式: "    (Default)    REG_SZ    C:\Python312\"
     */
    private function parseRegDefault(string $output): string
    {
        foreach (explode("\n", $output) as $line) {
            $line = trim($line);
            if (preg_match('/^\(Default\)\s+REG_\w+\s+(.+)$/i', $line, $m)) {
                return trim($m[1]);
            }
        }
        return '';
    }

    /**
     * 通过 where/which 定位可执行文件完整路径。
     */
    private function locateExecutable(string $cmd): string
    {
        $isWindows = strtoupper(substr(PHP_OS, 0, 3)) === 'WIN';

        // 尝试通过 PATH 目录遍历查找
        $execName = $isWindows ? $cmd . '.exe' : $cmd;
        $pathEnv = getenv('PATH');
        if ($pathEnv) {
            $dirs = explode(PATH_SEPARATOR, $pathEnv);
            foreach ($dirs as $dir) {
                $dir = preg_replace_callback('/%([^%]+)%/', function ($m) {
                    return getenv($m[1]) ?: $m[0];
                }, $dir);
                $fullPath = $dir . DIRECTORY_SEPARATOR . $execName;
                if (is_file($fullPath)) {
                    return $fullPath;
                }
            }
        }

        // 通过 where/which 命令查找
        if ($isWindows) {
            $output = @shell_exec('where ' . $cmd . ' 2>nul');
        } else {
            $output = @shell_exec('which ' . $cmd . ' 2>/dev/null');
        }
        if ($output) {
            $lines = explode("\n", trim($output));
            foreach ($lines as $line) {
                $line = trim($line);
                // Windows 下 WindowsApps 目录的 python.exe 因权限问题
                // is_file 可能返回 false，用 file_exists 作为补充检测
                if ($line) {
                    // 跳过 WindowsApps 目录的 python.exe（App Execution Aliases，
                    // 该目录权限受限且 is_file 不可靠）
                    if (stripos($line, 'WindowsApps') !== false) {
                        continue;
                    }
                    if (is_file($line)) {
                        return $line;
                    }
                }
            }
        }

        return '';
    }

    /**
     * 执行 --version 命令检测 Python 是否可用。
     */
    private function checkVersion(string $cmd): bool
    {
        // 使用 proc_open 更可靠地捕获输出和错误
        $descriptorspec = [
            0 => ['pipe', 'r'],  // stdin
            1 => ['pipe', 'w'],  // stdout
            2 => ['pipe', 'w'],  // stderr
        ];

        try {
            $process = @proc_open(
                $cmd . ' --version',
                $descriptorspec,
                $pipes
            );

            if (is_resource($process)) {
                fclose($pipes[0]);
                $stdout = stream_get_contents($pipes[1]);
                $stderr = stream_get_contents($pipes[2]);
                fclose($pipes[1]);
                fclose($pipes[2]);
                $returnCode = proc_close($process);

                $output = $stdout . $stderr;
                if ($returnCode === 0 && preg_match('/Python\s+[\d.]+/i', $output)) {
                    return true;
                }
            }
        } catch (\Throwable $e) {
            // fallback 到 shell_exec
        }

        // fallback: shell_exec 方式
        $output = @shell_exec($cmd . ' --version 2>&1');
        if ($output !== null && preg_match('/Python\s+[\d.]+/i', $output)) {
            return true;
        }

        return false;
    }

    /**
     * 获取拓展对应的可执行文件名。
     */
    private function getExecName(string $name): string
    {
        $map = [
            'python' => 'python.exe',
            'ffmpeg' => 'ffmpeg.exe',
        ];
        return $map[$name] ?? '';
    }
}
