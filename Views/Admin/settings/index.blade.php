<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>设置 - 本地服务</title>
    <link rel="stylesheet" href="{{ asset('CmsProUi/component/pear/css/pear.css') }}">
    <link rel="stylesheet" href="{{ asset('CmsProUi/font-awesome/4.7.0/css/font-awesome.min.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/admin.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/variables.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/reset.css') }}">
    <style>
        .settings-section { margin-bottom: 20px; }
        .settings-section .section-title { font-size: 16px; font-weight: 600; padding: 10px 0; border-bottom: 2px solid #1e9fff; margin-bottom: 15px; }
        .settings-item { margin-bottom: 15px; }
        .settings-item .item-label { display: inline-block; width: 140px; text-align: right; padding-right: 10px; color: #333; font-weight: 500; }
        .settings-item .item-tips { color: #999; font-size: 12px; margin-top: 4px; padding-left: 150px; }
        .settings-item .layui-input { width: 200px; }
        .settings-item .token-input { width: 400px; }
        .btn-area { padding: 20px 0 20px 150px; }
        .restart-tip { margin-top: 15px; padding: 10px 15px; background: #fff7e6; border: 1px solid #ffd591; border-radius: 4px; color: #d46b08; font-size: 13px; }
        .restart-tip i { margin-right: 6px; }
        .path-tip { margin-top: 10px; padding: 8px 12px; background: #fff7e6; border: 1px solid #ffd591; border-radius: 4px; font-size: 12px; color: #d46b08; line-height: 1.6; display: none; }
    </style>
</head>
<body>
<div class="pear-container" style="padding:15px;">
    <div class="layui-card">
        <div class="layui-card-body" style="padding:20px;">
            <form class="layui-form" lay-filter="settingsForm">
                <div class="settings-section">
                    <div class="section-title">基本配置</div>

                    <div class="settings-item">
                        <label class="item-label">监听端口</label>
                        <input type="number" name="port" class="layui-input" style="display:inline-block;" min="1" max="65535" placeholder="9501">
                        <div class="item-tips">本地 Go 服务 HTTP 监听端口，默认 9501</div>
                    </div>

                    <div class="settings-item" style="white-space:nowrap;">
                        <label class="item-label" style="white-space:normal;">认证 Token</label>
                        <input type="text" name="auth_token" class="layui-input token-input" readonly placeholder="加载中..." style="background:#f5f5f5;cursor:not-allowed;width:400px;display:inline-block;vertical-align:top;">
                        <button type="button" class="layui-btn layui-btn-sm layui-btn-warm" id="btnRegenToken" style="margin-left:8px;display:inline-block;vertical-align:top;"><i class="fa fa-refresh"></i> 重新生成</button>
                        <div class="item-tips" style="white-space:normal;">本地服务 API 认证凭证，修改后需重启服务生效</div>
                    </div>

                    <div class="settings-item">
                        <label class="item-label">日志级别</label>
                        <div class="layui-input-inline" style="width:200px;">
                            <select name="log_level">
                                <option value="debug">DEBUG（调试）</option>
                                <option value="info">INFO（信息）</option>
                                <option value="warn">WARN（警告）</option>
                                <option value="error">ERROR（错误）</option>
                            </select>
                        </div>
                        <div class="item-tips">日志记录详细程度，生产环境建议 INFO</div>
                    </div>

                    <div class="settings-item">
                        <label class="item-label">日志保留天数</label>
                        <input type="number" name="log_retention_days" class="layui-input" style="display:inline-block;" min="1" max="3650" placeholder="30">
                        <div class="item-tips">超过保留天数的日志将被自动清理，默认 30 天</div>
                    </div>

                    <div class="settings-item">
                        <label class="item-label">时区</label>
                        <div class="layui-input-inline" style="width:200px;">
                            <select name="timezone">
                                <option value="Asia/Shanghai">Asia/Shanghai（北京时间，UTC+8）</option>
                                <option value="Asia/Urumqi">Asia/Urumqi（乌鲁木齐时间，UTC+6）</option>
                                <option value="Asia/Hong_Kong">Asia/Hong_Kong（香港时间，UTC+8）</option>
                                <option value="Asia/Taipei">Asia/Taipei（台北时间，UTC+8）</option>
                                <option value="Asia/Tokyo">Asia/Tokyo（东京时间，UTC+9）</option>
                                <option value="Asia/Seoul">Asia/Seoul（首尔时间，UTC+9）</option>
                                <option value="Asia/Singapore">Asia/Singapore（新加坡时间，UTC+8）</option>
                                <option value="America/New_York">America/New_York（纽约时间，UTC-5）</option>
                                <option value="America/Los_Angeles">America/Los_Angeles（洛杉矶时间，UTC-8）</option>
                                <option value="Europe/London">Europe/London（伦敦时间，UTC+0）</option>
                                <option value="Europe/Berlin">Europe/Berlin（柏林时间，UTC+1）</option>
                                <option value="Australia/Sydney">Australia/Sydney（悉尼时间，UTC+10）</option>
                                <option value="UTC">UTC（协调世界时）</option>
                            </select>
                        </div>
                        <div class="item-tips">本地服务使用的时区，影响任务和日志的时间显示；修改后需重启服务生效</div>
                    </div>
                </div>

                <div class="btn-area">
                    <button class="layui-btn" lay-submit lay-filter="saveSettings"><i class="layui-icon layui-icon-ok"></i> 保存设置</button>
                    <button type="button" class="layui-btn layui-btn-normal" id="btnInstall"><i class="fa fa-download"></i> 生成安装脚本</button>
                    <button type="button" class="layui-btn layui-btn-warm" id="btnUninstall"><i class="fa fa-trash-o"></i> 生成卸载脚本</button>
                </div>

                <div class="path-tip" id="pathTip"></div>

                <hr style="margin:20px 0;">

                {{-- 开放 API 配置 --}}
                <div class="settings-section">
                    <div class="section-title">开放 API</div>

                    <div class="settings-item">
                        <label class="item-label">启用开关</label>
                        <div class="layui-input-inline" style="width:200px;">
                            <select name="open_api_enabled">
                                <option value="0">关闭</option>
                                <option value="1">开启</option>
                            </select>
                        </div>
                        <div class="item-tips">开启后，第三方可通过 <code>X-API-Key</code> 请求头调用本服务的开放 API</div>
                    </div>

                    <div class="settings-item" style="white-space:nowrap;">
                        <label class="item-label" style="white-space:normal;">API 密钥</label>
                        <input type="text" name="open_api_key" class="layui-input token-input" readonly placeholder="请先开启开放 API" style="background:#f5f5f5;cursor:not-allowed;width:400px;display:inline-block;vertical-align:top;">
                        <button type="button" class="layui-btn layui-btn-sm layui-btn-warm" id="btnRegenOpenApiKey" style="margin-left:8px;display:inline-block;vertical-align:top;"><i class="fa fa-refresh"></i> 重新生成</button>
                        <div class="item-tips" style="white-space:normal;">第三方调用时将此密钥放在 <code>X-API-Key</code> 请求头中</div>
                    </div>

                    <div style="margin-top:10px;padding:10px 15px;background:#fffbe6;border:1px solid #ffe58f;border-radius:4px;font-size:13px;line-height:1.8;">
                        <strong style="color:#d46b08;"><i class="fa fa-info-circle"></i> 开放 API 说明</strong>
                        <br>
                        开放 API 允许第三方跨服务器调用本服务的任务执行能力，适用于多服务器协同管理场景。
                        <br>
                        调用方式：
                        <code style="background:#f5f5f5;padding:1px 6px;border-radius:3px;">curl -H "X-API-Key: your-key" http://your-server:9501/api/open/task</code>
                        <br>
                        可用端点：
                        <ul style="margin:4px 0 0 20px;">
                            <li><code>POST /api/open/task</code> — 创建并执行任务</li>
                            <li><code>GET /api/open/tasks</code> — 任务列表</li>
                            <li><code>GET /api/open/task/{id}</code> — 任务详情</li>
                            <li><code>POST /api/open/task/{id}/cancel</code> — 取消任务</li>
                            <li><code>GET /api/open/status</code> — 服务状态</li>
                        </ul>
                    </div>
                </div>

                <div class="btn-area" style="padding-top:10px;">
                    <button class="layui-btn" lay-submit lay-filter="saveSettings"><i class="layui-icon layui-icon-ok"></i> 保存设置</button>
                </div>

                <div class="restart-tip">
                    <i class="fa fa-info-circle"></i> 修改端口或认证 Token 后，需<strong>重启本地服务</strong>方可生效。
                </div>
            </form>
        </div>
    </div>
</div>

<script src="{{ asset('CmsProUi/component/layui/layui.js') }}"></script>
<script src="{{ asset('CmsProUi/component/pear/pear.js') }}"></script>
<script>
layui.use(['form', 'layer', 'jquery'], function(){
    var form = layui.form, layer = layui.layer, $ = layui.jquery;
    var API = '/api/admin/cmspro/localservice';

    $.ajaxSetup({
        headers: { 'X-CSRF-TOKEN': '{{ csrf_token() }}' },
        statusCode: {
            401: function() {
                layer.msg('登录已过期，请重新登录', {icon: 2, time: 1500}, function() {
                    window.location.href = '/admin/login?redirect=' + encodeURIComponent(window.location.pathname);
                });
            }
        }
    });

    // 加载当前设置
    function loadSettings() {
        $.ajax({
            url: API + '/settings',
            type: 'GET',
            dataType: 'json',
            success: function(res) {
                if (res.code !== 0) {
                    layer.msg(res.message || '加载设置失败', {icon: 2, time: 2000});
                    return;
                }
                var d = res.data;
                if (d.port) $('input[name="port"]').val(d.port);
                if (d.auth_token) $('input[name="auth_token"]').val(d.auth_token);
                if (d.log_level) $('select[name="log_level"]').val(d.log_level);
                if (d.log_retention_days) $('input[name="log_retention_days"]').val(d.log_retention_days);
                if (d.open_api_enabled !== null) $('select[name="open_api_enabled"]').val(String(d.open_api_enabled));
                if (d.open_api_key) $('input[name="open_api_key"]').val(d.open_api_key);
                if (d.timezone) $('select[name="timezone"]').val(d.timezone);
                form.render();
            },
            error: function() {
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    }

    // 保存设置
    form.on('submit(saveSettings)', function(data) {
        var field = data.field;
        var loading = layer.load(1);

        $.ajax({
            url: API + '/settings',
            type: 'POST',
            dataType: 'json',
            contentType: 'application/json',
            data: JSON.stringify({
                port: parseInt(field.port),
                log_level: field.log_level,
                log_retention_days: parseInt(field.log_retention_days),
                open_api_enabled: parseInt(field.open_api_enabled || 0),
                open_api_key: field.open_api_key || '',
                timezone: field.timezone || 'Asia/Shanghai'
            }),
            success: function(res) {
                layer.close(loading);
                if (res.code === 0) {
                    layer.msg('保存成功', {icon: 1, time: 1500});
                    loadSettings();
                    // 时区变更需要重启服务才能生效
                    layer.open({
                        type: 0,
                        title: '提示',
                        content: '时区修改已保存，需重启本地服务方可生效。',
                        icon: 0,
                        time: 4000,
                        shadeClose: true
                    });
                } else {
                    layer.msg(res.message || '保存失败', {icon: 2, time: 2000});
                }
            },
            error: function() {
                layer.close(loading);
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
        return false;
    });

    // 重新生成 Token
    $('#btnRegenToken').click(function() {
        layer.confirm('确认重新生成认证 Token？\n修改后需重启本地服务方可生效。', {
            icon: 3,
            title: '确认操作'
        }, function(index) {
            var loading = layer.load(1);
            $.ajax({
                url: API + '/settings/regenerate-token',
                type: 'POST',
                dataType: 'json',
                success: function(res) {
                    layer.close(loading);
                    if (res.code === 0) {
                        var token = res.data.auth_token;
                        $('input[name="auth_token"]').val(token);
                        layer.msg('Token 已重新生成', {icon: 1, time: 2000});
                    } else {
                        layer.msg(res.message || '生成失败', {icon: 2, time: 2000});
                    }
                },
                error: function() {
                    layer.close(loading);
                    layer.msg('请求失败', {icon: 2, time: 2000});
                }
            });
            layer.close(index);
        });
    });

    // 重新生成开放 API 密钥
    $('#btnRegenOpenApiKey').click(function() {
        layer.confirm('确认重新生成开放 API 密钥？\n生成后需保存设置方可生效。', {
            icon: 3,
            title: '确认操作'
        }, function(index) {
            var loading = layer.load(1);
            $.ajax({
                url: API + '/settings/regenerate-open-api-key',
                type: 'POST',
                dataType: 'json',
                success: function(res) {
                    layer.close(loading);
                    if (res.code === 0) {
                        var key = res.data.open_api_key;
                        $('input[name="open_api_key"]').val(key);
                        layer.msg('API 密钥已重新生成', {icon: 1, time: 2000});
                    } else {
                        layer.msg(res.message || '生成失败', {icon: 2, time: 2000});
                    }
                },
                error: function() {
                    layer.close(loading);
                    layer.msg('请求失败', {icon: 2, time: 2000});
                }
            });
            layer.close(index);
        });
    });

    loadSettings();

    // 生成安装脚本
    $('#btnInstall').click(function() {
        var idx = layer.load(1, { content: '正在生成安装脚本（如需下载二进制文件请稍候）...', shade: [0.3, '#000'] });
        $.ajax({
            url: API + '/service/install-script',
            type: 'POST',
            dataType: 'json',
            success: function(res) {
                layer.close(idx);
                if (res.code === 0) {
                    var d = res.data;
                    var tip = $('#pathTip');
                    @if(PHP_OS_FAMILY === 'Windows')
                    tip.show().html(
                        '<i class="fa fa-info-circle"></i> 脚本已生成：<strong>' + d.script_path + '</strong><br>'
                        + '二进制文件：<strong>' + d.binary_path + '</strong><br>'
                        + '请<strong style="color:#c00;">右键</strong>以<strong>管理员身份运行</strong>该脚本安装服务。'
                    );
                    @else
                    tip.show().html(
                        '<i class="fa fa-info-circle"></i> 脚本已生成：<strong>' + d.script_path + '</strong><br>'
                        + '二进制文件：<strong>' + d.binary_path + '</strong><br>'
                        + '请以 <strong style="color:#c00;">root 权限</strong>执行：<code>sudo bash ' + d.script_path + '</code>'
                    );
                    @endif
                    layer.msg('安装脚本已生成', {icon: 1, time: 2000});
                } else {
                    layer.msg(res.message || '生成失败', {icon: 2, time: 2000});
                }
            },
            error: function() {
                layer.close(idx);
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    });

    // 生成卸载脚本
    $('#btnUninstall').click(function() {
        var idx = layer.load(1, { content: '正在生成卸载脚本（如需下载二进制文件请稍候）...', shade: [0.3, '#000'] });
        $.ajax({
            url: API + '/service/uninstall-script',
            type: 'POST',
            dataType: 'json',
            success: function(res) {
                layer.close(idx);
                if (res.code === 0) {
                    var d = res.data;
                    var tip = $('#pathTip');
                    @if(PHP_OS_FAMILY === 'Windows')
                    tip.show().html(
                        '<i class="fa fa-info-circle"></i> 卸载脚本已生成：<strong>' + d.script_path + '</strong><br>'
                        + '请<strong style="color:#c00;">右键</strong>以<strong>管理员身份运行</strong>该脚本卸载服务。'
                    );
                    @else
                    tip.show().html(
                        '<i class="fa fa-info-circle"></i> 卸载脚本已生成：<strong>' + d.script_path + '</strong><br>'
                        + '请以 <strong style="color:#c00;">root 权限</strong>执行：<code>sudo bash ' + d.script_path + '</code>'
                    );
                    @endif
                    layer.msg('卸载脚本已生成', {icon: 1, time: 2000});
                } else {
                    layer.msg(res.message || '生成失败', {icon: 2, time: 2000});
                }
            },
            error: function() {
                layer.close(idx);
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    });
});
</script>
</body>
</html>
