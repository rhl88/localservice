<?php

namespace App\Apps\CmsproLocalservice\Models;

use Illuminate\Database\Eloquent\Model;

/**
 * @property int $id
 * @property string $name 脚本标识
 * @property string $title 脚本名称
 * @property string $type 脚本类型: bat/ps1/sh/python
 * @property string $platform 适用平台: windows/linux/all
 * @property string $content 脚本内容
 * @property string|null $description 脚本说明
 * @property string|null $params 参数说明 JSON
 * @property int $status 1=启用 0=禁用
 * @property string $created_at
 * @property string $updated_at
 */
class CustomScript extends Model
{
    protected $table = 'app_cmspro_localservice_custom_scripts';

    protected $fillable = [
        'name', 'title', 'type', 'platform', 'content',
        'description', 'params', 'status',
    ];

    protected $casts = [
        'status' => 'integer',
        'created_at' => 'datetime:Y-m-d H:i:s',
        'updated_at' => 'datetime:Y-m-d H:i:s',
    ];
}
