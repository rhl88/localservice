<?php

namespace App\Apps\CmsproLocalservice\Models;

use Illuminate\Database\Eloquent\Model;

/**
 * MCP 访问凭据模型。
 *
 * 权限模型（与 Go 侧 internal/mcp/credentials.go 语义一致）：
 * - tool_ids 为已授权工具集，disabled 为显式禁用集，两者对已注册工具构成互斥分区
 * - 低风险工具默认授权；中/高风险默认不授权（opt-in，需在后台显式勾选）
 * - 显式禁用永不自动恢复
 * - 凭据支持启停（active）与有效期（expires_days，-1 永不过期）
 */
class McpKey extends Model
{
    protected $table = 'app_cmspro_localservice_mcp_keys';

    public const CREATED_AT = 'create_time';

    public const UPDATED_AT = 'update_time';

    /** 令牌前缀 */
    public const TOKEN_PREFIX = 'cmsmcp_';

    /** 凭据标识前缀 */
    public const KEY_ID_PREFIX = 'key_';

    /** 有效期选项：值 => 展示文案 */
    public const EXPIRES_OPTIONS = [
        -1 => '永不过期',
        7 => '7 天',
        30 => '30 天',
        90 => '90 天',
    ];

    protected $fillable = [
        'key_id', 'name', 'token', 'tool_ids', 'disabled', 'expires_days', 'active',
    ];

    protected $casts = [
        'expires_days' => 'integer',
        'active' => 'integer',
        'create_time' => 'datetime:Y-m-d H:i:s',
        'update_time' => 'datetime:Y-m-d H:i:s',
    ];

    protected $hidden = ['token'];

    /**
     * 已授权工具集
     *
     * @return array<int, string>
     */
    public function grantedTools(): array
    {
        return $this->decodeList($this->tool_ids);
    }

    /**
     * 显式禁用工具集
     *
     * @return array<int, string>
     */
    public function disabledTools(): array
    {
        return $this->decodeList($this->disabled);
    }

    /**
     * 是否已过期（create_time 缺失或无法解析时按失效处理，安全兜底）
     */
    public function isExpired(): bool
    {
        $days = (int) $this->expires_days;
        if ($days < 0) {
            return false;
        }

        // create_time 经 casts 为 Carbon；未落库实例（new McpKey([...])）可能仍是字符串
        $created = $this->create_time;
        if ($created === null || $created === '') {
            return true;
        }
        if (! $created instanceof \Carbon\CarbonInterface) {
            try {
                $created = \Illuminate\Support\Carbon::parse((string) $created);
            } catch (\Throwable $e) {
                return true;
            }
        }

        return $created->copy()->addDays($days)->isPast();
    }

    /**
     * 生效状态：启用且未过期
     */
    public function isEffective(): bool
    {
        return (int) $this->active === 1 && ! $this->isExpired();
    }

    /**
     * 解析生效工具集（已授权 ∩ 已注册，再去掉显式禁用项）
     *
     * @param  array<int, string>  $registeredIds  当前已注册工具标识
     * @return array<int, string>
     */
    public function effectiveTools(array $registeredIds): array
    {
        $registered = array_flip($registeredIds);
        $granted = [];
        foreach ($this->grantedTools() as $id) {
            if (isset($registered[$id])) {
                $granted[$id] = true;
            }
        }
        foreach ($this->disabledTools() as $id) {
            unset($granted[$id]);
        }

        // 按注册顺序输出，保证展示稳定
        $out = [];
        foreach ($registeredIds as $id) {
            if (isset($granted[$id])) {
                $out[] = $id;
            }
        }

        return $out;
    }

    /**
     * 令牌掩码（列表展示用，仅保留尾 4 位）
     */
    public function maskedToken(): string
    {
        $token = (string) $this->token;
        if ($token === '') {
            return '';
        }
        $tail = mb_strlen($token) > 4 ? mb_substr($token, -4) : '';

        return $tail !== '' ? '****'.$tail : '****';
    }

    /**
     * 生成唯一凭据标识
     */
    public static function generateKeyId(): string
    {
        do {
            $id = self::KEY_ID_PREFIX.bin2hex(random_bytes(6));
        } while (self::where('key_id', $id)->exists());

        return $id;
    }

    /**
     * 生成唯一访问令牌
     */
    public static function generateToken(): string
    {
        do {
            $token = self::TOKEN_PREFIX.rtrim(strtr(base64_encode(random_bytes(32)), '+/', 'AB'), '=');
        } while (self::where('token', $token)->exists());

        return $token;
    }

    /**
     * 按已授权集与已注册集推导禁用集（互斥分区）
     *
     * @param  array<int, string>  $granted
     * @param  array<int, string>  $registered
     * @return array{0: array<int, string>, 1: array<int, string>}  [规范化授权集, 禁用集]
     */
    public static function partitionTools(array $granted, array $registered): array
    {
        $want = array_flip($granted);
        $normalized = [];
        foreach ($registered as $id) {
            if (isset($want[$id])) {
                $normalized[] = $id;
            }
        }

        $grantedFlip = array_flip($normalized);
        $disabled = [];
        foreach ($registered as $id) {
            if (! isset($grantedFlip[$id])) {
                $disabled[] = $id;
            }
        }

        return [$normalized, $disabled];
    }

    /**
     * 解码 JSON 列表字段（兼容空值与非法内容）
     *
     * @return array<int, string>
     */
    protected function decodeList(mixed $value): array
    {
        if (is_array($value)) {
            return array_values(array_filter($value, 'is_string'));
        }
        if (! is_string($value) || trim($value) === '') {
            return [];
        }
        $decoded = json_decode($value, true);
        if (! is_array($decoded)) {
            return [];
        }

        return array_values(array_filter($decoded, 'is_string'));
    }
}