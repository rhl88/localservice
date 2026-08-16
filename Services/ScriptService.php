<?php

namespace App\Apps\CmsproLocalservice\Services;

use App\Apps\CmsproLocalservice\Models\CustomScript;
use Illuminate\Support\Facades\Log;

/**
 * 指令库管理：自定义脚本的 CRUD 与远程执行。
 */
class ScriptService
{
    public function __construct(
        private LocalServiceClient $client
    ) {}

    /**
     * 分页列表。
     */
    public function paginate(array $params = []): array
    {
        $query = CustomScript::query();

        if (!empty($params['keyword'])) {
            $keyword = $params['keyword'];
            $query->where(function ($q) use ($keyword) {
                $q->where('name', 'like', "%{$keyword}%")
                  ->orWhere('title', 'like', "%{$keyword}%");
            });
        }
        if (isset($params['type']) && $params['type'] !== '') {
            $query->where('type', $params['type']);
        }
        if (isset($params['platform']) && $params['platform'] !== '') {
            $query->where('platform', $params['platform']);
        }
        if (isset($params['status']) && $params['status'] !== '') {
            $query->where('status', (int) $params['status']);
        }

        $page = max(1, (int) ($params['page'] ?? 1));
        $pageSize = min(100, max(1, (int) ($params['page_size'] ?? 20)));

        $total = $query->count();
        $items = $query->orderBy('id', 'desc')
            ->skip(($page - 1) * $pageSize)
            ->take($pageSize)
            ->get()
            ->toArray();

        return ['items' => $items, 'total' => $total, 'page' => $page, 'page_size' => $pageSize];
    }

    /**
     * 全部启用指令列表（供下拉选择用）。
     */
    public function allEnabled(): array
    {
        return CustomScript::where('status', 1)
            ->orderBy('id', 'asc')
            ->get(['id', 'name', 'title', 'type', 'platform', 'description'])
            ->toArray();
    }

    /**
     * 单条详情。
     */
    public function find(int $id): ?CustomScript
    {
        return CustomScript::find($id);
    }

    /**
     * 创建。
     */
    public function create(array $data): CustomScript
    {
        return CustomScript::create($data);
    }

    /**
     * 更新。
     */
    public function update(int $id, array $data): ?CustomScript
    {
        $script = CustomScript::find($id);
        if (!$script) {
            return null;
        }
        $script->update($data);
        return $script->fresh();
    }

    /**
     * 校验同一平台内 name 是否唯一。
     *
     * @param string $name 脚本标识
     * @param string $platform 适用平台
     * @param int|null $excludeId 更新时排除自身 ID
     * @return bool 是否可用
     */
    public function isNameUniqueInPlatform(string $name, string $platform, ?int $excludeId = null): bool
    {
        $query = CustomScript::where('name', $name)->where('platform', $platform);
        if ($excludeId !== null) {
            $query->where('id', '!=', $excludeId);
        }
        return !$query->exists();
    }

    /**
     * 删除。
     */
    public function delete(int $id): bool
    {
        $script = CustomScript::find($id);
        if (!$script) {
            return false;
        }
        return $script->delete() ? true : false;
    }

    /**
     * 在本地服务中执行脚本。
     * 将脚本内容写入临时文件后通过 Go 端 system.script 任务执行。
     * 执行前校验脚本适用平台与当前运行环境是否匹配。
     *
     * @param int $id 脚本 ID
     * @param array $args 脚本参数键值对
     * @return array{code:int, message:string, data?:array}
     */
    public function execute(int $id, array $args = []): array
    {
        $script = CustomScript::find($id);
        if (!$script) {
            return ['code' => 404, 'message' => '脚本不存在', 'data' => null];
        }
        if ($script->status !== 1) {
            return ['code' => 403, 'message' => '脚本已禁用', 'data' => null];
        }

        // 平台匹配校验：platform=all 任意平台可执行，否则需匹配当前系统
        $currentPlatform = PHP_OS_FAMILY === 'Windows' ? 'windows' : 'linux';
        if ($script->platform !== 'all' && $script->platform !== $currentPlatform) {
            $platformLabel = ['windows' => 'Windows', 'linux' => 'Linux', 'all' => '全平台'];
            $scriptLabel = $platformLabel[$script->platform] ?? $script->platform;
            $currentLabel = $platformLabel[$currentPlatform] ?? $currentPlatform;
            return [
                'code' => 403,
                'message' => "该指令仅适用于{$scriptLabel}平台，当前系统为{$currentLabel}",
                'data' => null,
            ];
        }

        $content = $script->content;

        // 替换参数占位符 {{key}} 为实际值
        foreach ($args as $key => $value) {
            $content = str_replace(
                ['{{' . $key . '}}', '{% ' . $key . ' %}'],
                [$value, $value],
                $content
            );
        }

        // 通过 Go 端执行脚本
        // HTTP 超时需大于脚本执行超时，留 5 秒 buffer 给 Go 端收尾与网络往返
        $scriptTimeout = 60;
        $result = $this->client->exec('cmspro.localservice', 'system.script', [
            'script' => $content,
            'type' => $script->type,
            'timeout' => $scriptTimeout,
        ], $scriptTimeout + 5);

        if (($result['code'] ?? -1) !== 0) {
            $msg = $result['message'] ?? '执行失败';
            Log::warning('指令库执行失败', ['script_id' => $id, 'name' => $script->name, 'error' => $msg]);
            return ['code' => 500, 'message' => $msg, 'data' => $result['data'] ?? null];
        }

        // 将 stdout 从 GBK/GB2312 转换为 UTF-8（Windows 系统脚本输出为 GBK）
        if (isset($result['data']['stdout']) && is_string($result['data']['stdout'])) {
            $encoding = mb_detect_encoding($result['data']['stdout'], ['UTF-8', 'GBK', 'GB2312', 'CP936'], true);
            if ($encoding && $encoding !== 'UTF-8') {
                $converted = @mb_convert_encoding($result['data']['stdout'], 'UTF-8', $encoding);
                if ($converted !== false) {
                    $result['data']['stdout'] = $converted;
                }
            }
        }

        return ['code' => 0, 'message' => 'success', 'data' => $result['data']];
    }

    /**
     * 初始化 Linux 常用脚本库。
     * 返回成功插入的数量。
     */
    public function initDefaultLinuxScripts(): int
    {
        $defaults = $this->getDefaultLinuxScripts();
        $inserted = 0;
        foreach ($defaults as $item) {
            if (!$this->isNameUniqueInPlatform($item['name'], $item['platform'])) {
                continue;
            }
            $this->create($item);
            $inserted++;
        }
        return $inserted;
    }

    /**
     * 返回 Linux 系统常用 Shell 脚本列表。
     */
    public function getDefaultLinuxScripts(): array
    {
        return [
            // ──────────────────────────────────────────
            // 1. 系统信息
            // ──────────────────────────────────────────
            [
                'name' => 'sys_info',
                'title' => '系统信息概览',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '收集 CPU、内存、磁盘、网络等系统基础信息',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# 系统信息概览
echo "========== 系统信息 =========="
echo "主机名: $(hostname)"
echo "操作系统: $(uname -a)"
echo "运行时间: $(uptime -p)"
echo ""
echo "========== CPU 信息 =========="
echo "CPU 型号: $(grep 'model name' /proc/cpuinfo | head -1 | cut -d: -f2 | xargs)"
echo "CPU 核心数: $(nproc)"
echo "CPU 负载: $(uptime | awk -F'load average:' '{print $2}' | xargs)"
echo ""
echo "========== 内存信息 =========="
free -h
echo ""
echo "========== 磁盘信息 =========="
df -h --total | grep -v tmpfs | grep -v udev
echo ""
echo "========== 网络信息 =========="
ip addr show | grep 'inet ' | grep -v 127.0.0.1 | awk '{print $2, $NF}'
echo ""
echo "========== 运行进程 TOP10 =========="
ps aux --sort=-%mem | head -11
SHELL,
            ],
            // ──────────────────────────────────────────
            // 2. 磁盘检查
            // ──────────────────────────────────────────
            [
                'name' => 'disk_check',
                'title' => '磁盘空间检查',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '检查各分区磁盘使用率，超过 80% 标黄，超过 90% 标红',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# 磁盘空间检查 —— 按使用率分级告警
THRESHOLD_YELLOW=80
THRESHOLD_RED=90

echo "========== 磁盘空间检查 =========="
echo ""

df -h | grep '^/' | while read line; do
    usage=$(echo "$line" | awk '{print $5}' | sed 's/%//')
    mount=$(echo "$line" | awk '{print $6}')
    size=$(echo "$line" | awk '{print $2}')
    used=$(echo "$line" | awk '{print $3}')
    avail=$(echo "$line" | awk '{print $4}')

    if [ "$usage" -ge "$THRESHOLD_RED" ]; then
        echo -e "\033[31m[CRITICAL] $mount  已用 $usage%  总大小 $size  可用 $avail\033[0m"
    elif [ "$usage" -ge "$THRESHOLD_YELLOW" ]; then
        echo -e "\033[33m[WARNING]  $mount  已用 $usage%  总大小 $size  可用 $avail\033[0m"
    else
        echo "[OK]       $mount  已用 $usage%  总大小 $size  可用 $avail"
    fi
done

echo ""
echo "========== inode 检查 =========="
df -i | grep '^/' | while read line; do
    usage=$(echo "$line" | awk '{print $5}' | sed 's/%//')
    mount=$(echo "$line" | awk '{print $6}')
    if [ "$usage" -ge 90 ]; then
        echo -e "\033[31m[CRITICAL] inode $mount  已用 $usage%\033[0m"
    fi
done
SHELL,
            ],
            // ──────────────────────────────────────────
            // 3. 系统日志清理
            // ──────────────────────────────────────────
            [
                'name' => 'log_cleanup',
                'title' => '系统日志清理',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '清理 /var/log 下超过 {{days}} 天的日志文件，默认 7 天',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# 日志清理 —— 清理指定天数前的日志文件
DAYS="${1:-7}"
LOG_DIRS="/var/log /var/log/nginx /var/log/mysql"

echo "========== 日志文件清理 =========="
echo "清理 ${DAYS} 天前的日志文件"
echo ""

for dir in $LOG_DIRS; do
    if [ -d "$dir" ]; then
        count=$(find "$dir" -type f -mtime +$DAYS 2>/dev/null | wc -l)
        if [ "$count" -gt 0 ]; then
            find "$dir" -type f -mtime +$DAYS -exec rm -f {} \; 2>/dev/null
            echo "[OK] $dir: 清理 $count 个文件"
        else
            echo "[OK] $dir: 无需要清理的文件"
        fi
    fi
done

echo ""
echo "========== 清理 journalctl =========="
journalctl --vacuum-time=${DAYS}d 2>/dev/null && echo "[OK] journalctl 日志已清理" || echo "[INFO] journalctl 清理跳过（非 systemd 或无权限）"
SHELL,
            ],
            // ──────────────────────────────────────────
            // 4. Nginx 状态检查
            // ──────────────────────────────────────────
            [
                'name' => 'nginx_status',
                'title' => 'Nginx 状态检查',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '检查 Nginx 进程、端口、配置语法及访问统计',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# Nginx 状态检查
echo "========== Nginx 进程检查 =========="
if pgrep -x nginx > /dev/null; then
    echo -e "\033[32m[OK] Nginx 运行正常\033[0m"
    echo "进程数: $(pgrep -x nginx | wc -l)"
else
    echo -e "\033[31m[ERROR] Nginx 未运行\033[0m"
fi

echo ""
echo "========== Nginx 端口检查 =========="
for port in 80 443; do
    if ss -tlnp | grep -q ":$port "; then
        pid=$(ss -tlnp | grep ":$port " | grep -oP 'pid=\K[0-9]+')
        echo "[OK] 端口 $port 已监听 (PID: $pid)"
    else
        echo "[WARN] 端口 $port 未监听"
    fi
done

echo ""
echo "========== 配置语法检查 =========="
nginx -t 2>&1 && echo -e "\033[32m[OK] 配置语法正确\033[0m" || echo -e "\033[31m[ERROR] 配置语法错误\033[0m"

echo ""
echo "========== Nginx 访问统计 =========="
LOG="/var/log/nginx/access.log"
if [ -f "$LOG" ]; then
    echo "总请求数: $(wc -l < "$LOG")"
    echo "状态码 200: $(grep -c ' 200 ' "$LOG")"
    echo "状态码 404: $(grep -c ' 404 ' "$LOG")"
    echo "状态码 500: $(grep -c ' 500 ' "$LOG")"
    echo ""
    echo "TOP10 IP:"
    awk '{print $1}' "$LOG" | sort | uniq -c | sort -rn | head -10
fi
SHELL,
            ],
            // ──────────────────────────────────────────
            // 5. MySQL 数据库备份
            // ──────────────────────────────────────────
            [
                'name' => 'mysql_backup',
                'title' => 'MySQL 数据库备份',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '使用 mysqldump 备份指定数据库，保留 {{keep_days}} 天，默认 7 天',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# MySQL 数据库备份
# 参数: db_host, db_port, db_user, db_pass, db_name, backup_dir, keep_days

DB_HOST="${db_host:-localhost}"
DB_PORT="${db_port:-3306}"
DB_USER="${db_user:-root}"
DB_PASS="${db_pass}"
DB_NAME="${db_name:-all}"
BACKUP_DIR="${backup_dir:-/data/backup/mysql}"
KEEP_DAYS="${keep_days:-7}"

echo "========== MySQL 数据库备份 =========="
echo "主机: ${DB_HOST}:${DB_PORT}"
echo "备份目录: ${BACKUP_DIR}"
echo "保留天数: ${KEEP_DAYS}"
echo ""

# 创建备份目录
mkdir -p "$BACKUP_DIR"

# 备份脚本
DUMP_OPTS="--host=${DB_HOST} --port=${DB_PORT} --user=${DB_USER} --single-transaction --routines --events --triggers --quick"
if [ -n "$DB_PASS" ]; then
    export MYSQL_PWD="$DB_PASS"
fi

DATE_TAG=$(date +%Y%m%d_%H%M%S)

if [ "$DB_NAME" = "all" ]; then
    # 备份所有数据库（排除系统库）
    EXCLUDE_DBS="mysql|information_schema|performance_schema|sys"
    dbs=$(mysql --host="${DB_HOST}" --port="${DB_PORT}" --user="${DB_USER}" -e "SHOW DATABASES" 2>/dev/null | grep -Ev "(${EXCLUDE_DBS)|Database")
    for db in $dbs; do
        BACKUP_FILE="${BACKUP_DIR}/${db}_${DATE_TAG}.sql.gz"
        mysqldump ${DUMP_OPTS} "$db" 2>/dev/null | gzip > "$BACKUP_FILE"
        echo "[OK] 数据库 $db 备份完成: $(du -h "$BACKUP_FILE" | cut -f1)"
    done
else
    BACKUP_FILE="${BACKUP_DIR}/${DB_NAME}_${DATE_TAG}.sql.gz"
    mysqldump ${DUMP_OPTS} "$DB_NAME" 2>/dev/null | gzip > "$BACKUP_FILE"
    echo "[OK] 数据库 ${DB_NAME} 备份完成: $(du -h "$BACKUP_FILE" | cut -f1)"
fi

unset MYSQL_PWD

# 清理过期备份
echo ""
echo "清理 ${KEEP_DAYS} 天前的备份..."
find "$BACKUP_DIR" -name "*.sql.gz" -type f -mtime +${KEEP_DAYS} -delete
echo "[OK] 清理完成"
echo ""
echo "备份目录当前文件列表:"
ls -lh "$BACKUP_DIR"
SHELL,
            ],
            // ──────────────────────────────────────────
            // 6. SSL 证书检查
            // ──────────────────────────────────────────
            [
                'name' => 'ssl_check',
                'title' => 'SSL 证书到期检查',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '检查指定域名的 SSL 证书到期天数，低于阈值时告警',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# SSL 证书到期检查
# 参数: domains（逗号分隔）, warn_days（默认 30）

DOMAINS="${domains}"
WARN_DAYS="${warn_days:-30}"

if [ -z "$DOMAINS" ]; then
    echo "请指定域名参数 domains，如 domains=example.com,google.com"
    echo "用法示例："
    echo "  bash ssl_check.sh domains=example.com,google.com warn_days=30"
    exit 1
fi

echo "========== SSL 证书到期检查 =========="
echo "检查域名: ${DOMAINS}"
echo "告警阈值: ${WARN_DAYS} 天"
echo ""

IFS=',' read -ra ADDR <<< "$DOMAINS"
for domain in "${ADDR[@]}"; do
    domain=$(echo "$domain" | xargs)
    if [ -z "$domain" ]; then continue; fi

    expiry_date=$(echo | openssl s_client -servername "$domain" -connect "${domain}:443" 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
    if [ -z "$expiry_date" ]; then
        echo -e "\033[31m[ERROR] $domain: 无法获取证书信息\033[0m"
        continue
    fi

    expiry_epoch=$(date -d "$expiry_date" +%s 2>/dev/null)
    now_epoch=$(date +%s)
    days_left=$(( (expiry_epoch - now_epoch) / 86400 ))

    if [ "$days_left" -lt 0 ]; then
        echo -e "\033[31m[EXPIRED] $domain: 已过期 $(( -days_left )) 天\033[0m"
    elif [ "$days_left" -lt "$WARN_DAYS" ]; then
        echo -e "\033[33m[WARNING] $domain: 还有 ${days_left} 天到期\033[0m"
    else
        echo -e "\033[32m[OK] $domain: 还有 ${days_left} 天到期\033[0m"
    fi
done
SHELL,
            ],
            // ──────────────────────────────────────────
            // 7. 服务启停管理
            // ──────────────────────────────────────────
            [
                'name' => 'service_manager',
                'title' => '服务批量启停',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '批量启动/停止/重启系统服务（支持 nginx/php-fpm/mysql/redis）',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# 服务批量启停
# 参数: action(start|stop|restart|status), services(逗号分隔，默认都操作)

ACTION="${action:-status}"
SERVICES="${services:-nginx php-fpm mysql redis}"
ALLOWED_ACTIONS="start stop restart status"

if ! echo "$ALLOWED_ACTIONS" | grep -wq "$ACTION"; then
    echo "错误: action 必须是 start|stop|restart|status"
    exit 1
fi

echo "========== 服务批量管理 =========="
echo "操作: ${ACTION}"
echo "服务列表: ${SERVICES}"
echo ""

IFS=' ' read -ra SVC_LIST <<< "$SERVICES"
for svc in "${SVC_LIST[@]}"; do
    svc=$(echo "$svc" | xargs)
    if [ -z "$svc" ]; then continue; fi

    echo -n "[$svc] "
    case "$ACTION" in
        status)
            if systemctl is-active "$svc" &>/dev/null; then
                echo -e "\033[32m运行中\033[0m"
            else
                echo -e "\033[31m未运行\033[0m"
            fi
            ;;
        start|stop|restart)
            systemctl "$ACTION" "$svc" 2>&1 && \
                echo -e "\033[32m${ACTION} 成功\033[0m" || \
                echo -e "\033[31m${ACTION} 失败\033[0m"
            ;;
    esac
done
SHELL,
            ],
            // ──────────────────────────────────────────
            // 8. 防火墙规则管理
            // ──────────────────────────────────────────
            [
                'name' => 'firewall_mgr',
                'title' => '防火墙规则管理',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '查看/添加/删除 iptables 或 ufw 防火墙规则',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# 防火墙规则管理
# 参数: action(list|allow|deny|delete), port, protocol(tcp|udp), source(IP)

ACTION="${action:-list}"
PORT="${port}"
PROTOCOL="${protocol:-tcp}"
SOURCE="${source:-0.0.0.0/0}"

echo "========== 防火墙规则管理 =========="
echo "操作: ${ACTION}"
echo ""

# 检测防火墙类型
if command -v ufw &>/dev/null; then
    FW="ufw"
elif command -v firewall-cmd &>/dev/null; then
    FW="firewalld"
else
    FW="iptables"
fi
echo "防火墙类型: ${FW}"
echo ""

case "$FW" in
    ufw)
        case "$ACTION" in
            list)
                ufw status numbered
                ;;
            allow)
                if [ -n "$PORT" ]; then
                    ufw allow proto "$PROTOCOL" to any port "$PORT" from "$SOURCE"
                    echo "[OK] 已添加允许规则: $PROTOCOL/$PORT <- $SOURCE"
                else
                    echo "请指定 port 参数"
                fi
                ;;
            deny)
                if [ -n "$PORT" ]; then
                    ufw deny proto "$PROTOCOL" to any port "$PORT" from "$SOURCE"
                    echo "[OK] 已添加拒绝规则: $PROTOCOL/$PORT <- $SOURCE"
                else
                    echo "请指定 port 参数"
                fi
                ;;
            delete)
                if [ -n "$PORT" ]; then
                    ufw delete allow "$PORT/$PROTOCOL"
                else
                    echo "请指定 port 参数"
                fi
                ;;
        esac
        ;;
    iptables)
        case "$ACTION" in
            list)
                iptables -L -n --line-numbers
                ;;
            allow)
                if [ -n "$PORT" ]; then
                    iptables -A INPUT -p "$PROTOCOL" --dport "$PORT" -s "$SOURCE" -j ACCEPT
                    echo "[OK] 已添加 iptables 允许规则"
                fi
                ;;
            deny)
                if [ -n "$PORT" ]; then
                    iptables -A INPUT -p "$PROTOCOL" --dport "$PORT" -s "$SOURCE" -j DROP
                    echo "[OK] 已添加 iptables 拒绝规则"
                fi
                ;;
        esac
        ;;
esac
SHELL,
            ],
            // ──────────────────────────────────────────
            // 9. 网络连通性检测
            // ──────────────────────────────────────────
            [
                'name' => 'network_diag',
                'title' => '网络连通性检测',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '测试目标主机/端口的连通性（ping + telnet + curl）',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# 网络连通性检测
# 参数: target(目标地址), port(目标端口), count(Ping次数)

TARGET="${target:-8.8.8.8}"
PORT="${port:-80}"
COUNT="${count:-3}"

echo "========== 网络连通性检测 =========="
echo "目标主机: ${TARGET}"
echo "目标端口: ${PORT}"
echo ""

echo "--- 1. Ping 测试 ---"
ping -c "$COUNT" -W 2 "$TARGET" 2>&1
echo ""

echo "--- 2. 端口连通性测试 ---"
if command -v nc &>/dev/null; then
    nc -zv -w 3 "$TARGET" "$PORT" 2>&1 && echo -e "\033[32m[OK] 端口 ${PORT} 可达\033[0m" || echo -e "\033[31m[FAIL] 端口 ${PORT} 不可达\033[0m"
elif command -v telnet &>/dev/null; then
    timeout 3 telnet "$TARGET" "$PORT" 2>&1 | head -5
else
    echo "[WARN] 未安装 nc 或 telnet，跳过端口检测"
fi
echo ""

echo "--- 3. HTTP 状态检测 ---"
if command -v curl &>/dev/null; then
    http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "http://${TARGET}:${PORT}" 2>/dev/null)
    echo "HTTP 状态码: ${http_code:-连接失败}"
else
    echo "[WARN] 未安装 curl，跳过 HTTP 检测"
fi
echo ""

echo "--- 4. DNS 解析 ---"
nslookup "$TARGET" 2>&1 | head -10
echo ""
echo "--- Traceroute（前 5 跳）---"
traceroute -m 5 -n "$TARGET" 2>&1 || echo "[WARN] traceroute 不可用"
SHELL,
            ],
            // ──────────────────────────────────────────
            // 10. PHP 项目部署
            // ──────────────────────────────────────────
            [
                'name' => 'php_deploy',
                'title' => 'PHP 项目部署',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '从 Git 仓库拉取 PHP 项目，安装 Composer 依赖，设置目录权限',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# PHP 项目部署脚本
# 参数: repo_url, branch, deploy_dir, php_version, composer_opts

REPO_URL="${repo_url}"
BRANCH="${branch:-main}"
DEPLOY_DIR="${deploy_dir:-/data/www/project}"
PHP_VERSION="${php_version:-8.2}"
COMPOSER_OPTS="${composer_opts:---no-dev --optimize-autoloader}"

if [ -z "$REPO_URL" ]; then
    echo "请指定 repo_url 参数（Git 仓库地址）"
    exit 1
fi

echo "========== PHP 项目部署 =========="
echo "仓库: ${REPO_URL}"
echo "分支: ${BRANCH}"
echo "部署目录: ${DEPLOY_DIR}"
echo "PHP 版本: ${PHP_VERSION}"
echo ""

# 1. 拉取代码
if [ -d "${DEPLOY_DIR}/.git" ]; then
    echo "--- 更新代码 ---"
    cd "$DEPLOY_DIR"
    git fetch --all
    git checkout "$BRANCH"
    git pull origin "$BRANCH"
else
    echo "--- 克隆代码 ---"
    mkdir -p "$DEPLOY_DIR"
    git clone -b "$BRANCH" "$REPO_URL" "$DEPLOY_DIR"
fi

echo "[OK] 代码获取完成"
echo ""

# 2. Composer 安装
cd "$DEPLOY_DIR"
if [ -f "composer.json" ]; then
    echo "--- Composer 依赖安装 ---"
    if command -v "php${PHP_VERSION}" &>/dev/null; then
        PHP_BIN="php${PHP_VERSION}"
    else
        PHP_BIN="php"
    fi

    if command -v composer &>/dev/null; then
        composer install $COMPOSER_OPTS --no-interaction 2>&1
    elif [ -f "composer.phar" ]; then
        $PHP_BIN composer.phar install $COMPOSER_OPTS --no-interaction 2>&1
    else
        curl -sS https://getcomposer.org/installer | $PHP_BIN
        $PHP_BIN composer.phar install $COMPOSER_OPTS --no-interaction 2>&1
    fi
    echo "[OK] Composer 依赖安装完成"
fi

echo ""

# 3. 目录权限设置
echo "--- 设置目录权限 ---"
chown -R www:www "$DEPLOY_DIR" 2>/dev/null || chown -R nobody:nobody "$DEPLOY_DIR" 2>/dev/null
chmod -R 755 "$DEPLOY_DIR"
chmod -R 775 "${DEPLOY_DIR}/storage" 2>/dev/null
chmod -R 775 "${DEPLOY_DIR}/bootstrap/cache" 2>/dev/null
echo "[OK] 目录权限设置完成"

echo ""

# 4. 环境检查
echo "--- 环境检查 ---"
$PHP_BIN -v | head -1
echo "PHP: $(which $PHP_BIN)"
echo "Composer: $(composer --version 2>/dev/null || echo '未安装')"
echo "Git: $(git --version 2>/dev/null || echo '未安装')"
echo ""
echo -e "\033[32m========== 部署完成 ==========\033[0m"
SHELL,
            ],
            // ──────────────────────────────────────────
            // 11. 进程资源监控
            // ──────────────────────────────────────────
            [
                'name' => 'process_monitor',
                'title' => '进程资源监控',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '按 CPU/内存排序查看系统进程 TOP，支持进程名过滤',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# 进程资源监控
# 参数: sort_by(cpu|mem), filter(进程名关键字), count(显示条数)

SORT_BY="${sort_by:-mem}"
FILTER="${filter}"
COUNT="${count:-15}"

echo "========== 进程资源监控 =========="
echo "排序: ${SORT_BY}"
echo "过滤: ${FILTER:-无}"
echo ""

case "$SORT_BY" in
    cpu)
        SORT_OPT="-k3 -r"
        HEADER="排序方式: CPU 使用率"
        ;;
    mem)
        SORT_OPT="-k4 -r"
        HEADER="排序方式: 内存使用率"
        ;;
    *)
        SORT_OPT="-k4 -r"
        HEADER="排序方式: 内存使用率"
        ;;
esac

echo "--- TOP ${COUNT} ${HEADER} ---"
if [ -n "$FILTER" ]; then
    ps aux | grep -v grep | grep "$FILTER" | sort -n $SORT_OPT | head -n "$COUNT"
else
    ps aux | sort -n $SORT_OPT | head -n "$COUNT"
fi

echo ""
echo "--- 总进程数 ---"
ps aux | wc -l
echo ""

echo "--- 按用户统计 ---"
ps aux | awk '{print $1}' | sort | uniq -c | sort -rn | head -10

echo ""
echo "--- 僵尸进程 ---"
zombie_count=$(ps aux | awk '{print $8}' | grep -c Z)
echo "僵尸进程数: ${zombie_count}"
if [ "$zombie_count" -gt 0 ]; then
    echo "详细信息:"
    ps aux | awk '{if($8 == "Z") print $0}'
fi
SHELL,
            ],
            // ──────────────────────────────────────────
            // 12. 定时任务管理
            // ──────────────────────────────────────────
            [
                'name' => 'cron_manager',
                'title' => 'Crontab 任务管理',
                'type' => 'sh',
                'platform' => 'linux',
                'description' => '查看/添加/备份 crontab 定时任务',
                'status' => 1,
                'content' => <<<'SHELL'
#!/bin/bash
# Crontab 任务管理
# 参数: action(list|add|backup|clear), cron_expr(定时表达式), cron_cmd(执行的命令), user

ACTION="${action:-list}"
CRON_EXPR="${cron_expr}"
CRON_CMD="${cron_cmd}"
CRON_USER="${cron_user:-$USER}"
BACKUP_DIR="${backup_dir:-/data/backup/crontab}"

echo "========== Crontab 任务管理 =========="
echo "操作: ${ACTION}"
echo "用户: ${CRON_USER}"
echo ""

case "$ACTION" in
    list)
        echo "当前用户的 crontab 任务:"
        echo "---"
        crontab -l 2>/dev/null || echo "(无定时任务)"
        ;;
    add)
        if [ -z "$CRON_EXPR" ] || [ -z "$CRON_CMD" ]; then
            echo "错误: 添加任务需要 cron_expr 和 cron_cmd 参数"
            echo "示例: cron_expr='0 3 * * *' cron_cmd='/usr/local/bin/backup.sh'"
            exit 1
        fi
        (crontab -l 2>/dev/null; echo "$CRON_EXPR $CRON_CMD") | crontab -
        echo "[OK] 已添加定时任务:"
        echo "  ${CRON_EXPR} ${CRON_CMD}"
        ;;
    backup)
        mkdir -p "$BACKUP_DIR"
        BACKUP_FILE="${BACKUP_DIR}/crontab_$(date +%Y%m%d_%H%M%S).bak"
        crontab -l > "$BACKUP_FILE" 2>/dev/null
        if [ -s "$BACKUP_FILE" ]; then
            echo "[OK] 备份已保存: ${BACKUP_FILE}"
        else
            echo "[WARN] 当前无 crontab 任务，备份为空文件"
            rm -f "$BACKUP_FILE"
        fi
        ;;
    clear)
        crontab -r 2>/dev/null
        echo "[OK] 已清除所有 crontab 任务"
        ;;
esac
SHELL,
            ],
        ];
    }
}
