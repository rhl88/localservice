<?php

return [
    // 默认监听端口
    'port' => 9501,
    // 认证 Token（安装时生成后写入 config_items，此处仅为回退默认）
    'auth_token' => '',
    // 日志级别
    'log_level' => 'info',
    // 日志保留天数
    'log_retention_days' => 30,
    // Windows 服务名称（与 Go 服务注册名一致）
    'service_name' => 'CmsProLocalService',
    // 服务显示名称
    'service_display_name' => 'CmsPro 本地服务',
    // Go 二进制相对应用根目录路径
    'binary' => 'Bin/windows/cmspro-local-service.exe',
    // 调用方 app_id（管理界面自身发起的操作用此标识）
    'self_app_id' => 'cmspro.localservice',
    // HTTP 请求超时（秒）
    'timeout' => 30,
    // 时区（Go 服务默认 Asia/Shanghai）
    'timezone' => 'Asia/Shanghai',
];
