<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>拓展管理 - 本地服务</title>
    <link rel="stylesheet" href="{{ asset('CmsProUi/component/pear/css/pear.css') }}">
    <link rel="stylesheet" href="{{ asset('CmsProUi/font-awesome/4.7.0/css/font-awesome.min.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/admin.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/variables.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/reset.css') }}">
    <style>
        .edit-form-label { display: inline-block; width: 80px; text-align: right; padding-right: 10px; color: #333; }
        .edit-form-item { margin-bottom: 12px; }
        .test-result-success { padding: 10px; background: #f6ffed; border: 1px solid #b7eb8f; border-radius: 4px; color: #52c41a; }
        .test-result-fail { padding: 10px; background: #fff2f0; border: 1px solid #ffccc7; border-radius: 4px; color: #ff4d4f; }
    </style>
</head>
<body>
<div class="pear-container">
    <div class="layui-card">
        <div class="layui-card-body">
            <table id="extTable" lay-filter="extTable"></table>
        </div>
    </div>
</div>

<script type="text/html" id="statusTpl">
@verbatim
{{# if(d.status === 1){ }}
<span class="layui-badge layui-bg-green">启用</span>
{{# } else { }}
<span class="layui-badge layui-bg-gray">禁用</span>
{{# } }}
@endverbatim
</script>

<script type="text/html" id="pathTpl">
@verbatim
{{# if(d.path && d.path !== ''){ }}
<span title="{{ d.path }}">{{ d.path }}</span>
{{# } else { }}
<span style="color:#999;">未安装</span>
{{# } }}
@endverbatim
</script>

<script type="text/html" id="extBar">
@verbatim
{{# if(d.path && d.path !== ''){ }}
<a class="layui-btn layui-btn-xs" lay-event="config">配置</a>
<a class="layui-btn layui-btn-xs layui-btn-warm" lay-event="test">测试</a>
{{# } else { }}
<a class="layui-btn layui-btn-xs layui-btn-normal" lay-event="install"><i class="fa fa-download"></i> 安装</a>
<a class="layui-btn layui-btn-xs" lay-event="config">配置</a>
<a class="layui-btn layui-btn-xs layui-btn-warm" lay-event="test">测试</a>
{{# } }}
@endverbatim
</script>

<script src="{{ asset('CmsProUi/component/layui/layui.js') }}"></script>
<script src="{{ asset('CmsProUi/component/pear/pear.js') }}"></script>
<script>
layui.use(['table', 'layer', 'form', 'jquery'], function(){
    var table = layui.table, layer = layui.layer, form = layui.form, $ = layui.jquery;
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

    function loadData() {
        $.ajax({
            url: API + '/extensions',
            type: 'GET',
            dataType: 'json',
            success: function(res) {
                if (res.code !== 0) {
                    layer.msg(res.message || '加载失败', {icon: 2, time: 2000});
                    return;
                }
                var items = res.data || [];
                table.render({
                    elem: '#extTable',
                    data: items,
                    cols: [[
                        {field: 'name', title: '标识', width: 100},
                        {field: 'title', title: '名称', width: 120},
                        {field: 'type', title: '类型', width: 100},
                        {field: 'path', title: '路径', minWidth: 250, templet: '#pathTpl'},
                        {field: 'status', title: '状态', width: 80, templet: '#statusTpl'},
                        {title: '操作', width: 200, toolbar: '#extBar', fixed: 'right'}
                    ]],
                    page: false
                });
            },
            error: function() {
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    }

    // 执行安装请求
    function doInstall(name, title, path) {
        var idx = layer.load(1, { content: '正在检测并安装...', shade: [0.3, '#000'] });
        var data = {};
        if (path) data.path = path;
        $.ajax({
            url: API + '/extensions/' + encodeURIComponent(name) + '/install',
            type: 'POST',
            dataType: 'json',
            data: data,
            success: function(res) {
                layer.close(idx);
                if (res.code === 0) {
                    layer.msg('安装成功', {icon: 1, time: 1500});
                    loadData();
                } else {
                    // 未检测到，弹出手动配置对话框
                    showManualInstall(name, title, res.message || '未检测到安装路径');
                }
            },
            error: function() {
                layer.close(idx);
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    }

    // 通过指令后台安装（异步 + 轮询）
    function doInstallByCommand(name, title, command) {
        var idx = layer.load(2, { content: '<span style="font-size:14px;">正在提交安装任务...</span>', shade: [0.3, '#000'] });
        $.ajax({
            url: API + '/extensions/' + encodeURIComponent(name) + '/install-by-command',
            type: 'POST',
            dataType: 'json',
            contentType: 'application/json',
            data: JSON.stringify({ command: command }),
            success: function(res) {
                layer.close(idx);
                if (res.code === 0) {
                    var taskId = res.data.task_id;
                    // 打开等待弹窗
                    var waitIndex = layer.open({
                        type: 1,
                        title: '安装执行中 - ' + escapeHtml(title || name),
                        area: ['500px', '180px'],
                        shade: 0.3,
                        content: '<div style="padding:30px;text-align:center;">'
                            + '<i class="fa fa-spinner fa-pulse" style="font-size:32px;color:#2d8cf0;"></i>'
                            + '<p style="margin-top:15px;color:#666;font-size:14px;">后台安装任务已提交，正在等待执行完成...</p>'
                            + '<p style="margin-top:8px;color:#999;font-size:12px;">任务 ID: ' + escapeHtml(taskId) + '</p>'
                            + '</div>',
                        btn: ['关闭'],
                        yes: function(index) {
                            layer.close(index);
                            pollInstallTask(name, title, taskId, null);
                        }
                    });
                    // 开始轮询
                    pollInstallTask(name, title, taskId, waitIndex);
                } else {
                    layer.msg(res.message || '提交安装任务失败', {icon: 2, time: 3000});
                }
            },
            error: function() {
                layer.close(idx);
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    }

    // 轮询安装任务结果
    function pollInstallTask(name, title, taskId, waitLayerIndex) {
        $.ajax({
            url: API + '/extensions/' + encodeURIComponent(name) + '/install-task/' + encodeURIComponent(taskId),
            type: 'GET',
            dataType: 'json',
            success: function(res) {
                if (res.code !== 0) {
                    if (waitLayerIndex) layer.close(waitLayerIndex);
                    layer.msg('查询任务状态失败', {icon: 2, time: 2000});
                    return;
                }

                var data = res.data || {};
                var status = data.status || 'unknown';

                if (status === 'running' || status === 'pending') {
                    // 任务还未完成，1秒后继续轮询
                    setTimeout(function() {
                        pollInstallTask(name, title, taskId, waitLayerIndex);
                    }, 1000);
                    return;
                }

                // 任务完成
                if (waitLayerIndex) layer.close(waitLayerIndex);

                var isSuccess = (status === 'success');
                var msg = '指令执行完成，退出码: ' + (data.exit_code ?? '?');
                if (data.detected_path) {
                    msg += '<br>已自动检测到路径: ' + escapeHtml(data.detected_path);
                }
                var stdoutHtml = '';
                if (data.stdout) {
                    stdoutHtml = '<div style="margin-top:10px;padding:8px;background:#1e1e1e;color:#d4d4d4;border-radius:4px;font-family:monospace;font-size:12px;max-height:200px;overflow:auto;white-space:pre-wrap;word-break:break-all;">'
                        + escapeHtml(data.stdout)
                        + '</div>';
                }
                var bgClass = isSuccess ? '#f6ffed' : '#fff2f0';
                var borderClass = isSuccess ? '#b7eb8f' : '#ffccc7';
                var colorClass = isSuccess ? '#52c41a' : '#ff4d4f';
                var iconClass = isSuccess ? 'fa-check-circle' : 'fa-times-circle';

                layer.open({
                    type: 1,
                    title: '安装结果 - ' + escapeHtml(title || name),
                    area: ['600px', '420px'],
                    shade: 0.3,
                    content: '<div style="padding:15px;"><div style="padding:10px;background:' + bgClass + ';border:1px solid ' + borderClass + ';border-radius:4px;color:' + colorClass + ';">'
                        + '<i class="fa ' + iconClass + '"></i> ' + msg
                        + '</div>' + stdoutHtml + '</div>',
                    btn: ['确定'],
                    yes: function(index) {
                        layer.close(index);
                        loadData();
                    },
                    end: function() {
                        loadData();
                    }
                });
            },
            error: function() {
                if (waitLayerIndex) layer.close(waitLayerIndex);
                layer.msg('查询任务状态请求失败', {icon: 2, time: 2000});
            }
        });
    }

    // 手动配置弹窗
    function showManualInstall(name, title, hint) {
        var html = '<div style="padding:15px;">'
            + '<div style="margin-bottom:12px;color:#d46b08;background:#fff7e6;border:1px solid #ffd591;border-radius:4px;padding:8px 12px;font-size:13px;">'
            + '<i class="fa fa-info-circle"></i> ' + escapeHtml(hint)
            + '</div>'
            // 方式一：手动填写路径
            + '<fieldset class="layui-elem-field" style="margin-bottom:12px;"><legend style="font-size:13px;font-weight:bold;">方式一：手动填写安装路径</legend>'
            + '<div style="padding:8px 0;">'
            + '<form class="layui-form" lay-filter="manualInstallForm">'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">安装路径</label>'
            + '<div class="layui-input-inline" style="width:300px;">'
            + '<input type="text" name="install_path" class="layui-input" placeholder="请输入 ' + escapeHtml(title || name) + ' 的可执行文件路径">'
            + '</div></div>'
            + '</form>'
            + '<div style="padding-left:90px;margin-top:10px;">'
            + '<button class="layui-btn layui-btn-normal layui-btn-sm" id="btnManualInstall"><i class="fa fa-check"></i> 确认安装</button>'
            + '</div></div></fieldset>'
            // 方式二：指令后台安装
            + '<fieldset class="layui-elem-field" style="border-color:#dce5f0;"><legend style="font-size:13px;font-weight:bold;color:#2d8cf0;">方式二：使用指令后台安装</legend>'
            + '<div style="padding:8px 0;">'
            + '<div id="suggestCommands" style="margin-bottom:8px;"></div>'
            + '<div style="background:#f8fafc;border:1px solid #e8eaec;border-radius:4px;padding:8px 10px;font-family:\'Cascadia Code\',\'Consolas\',monospace;font-size:13px;">'
            + '<div style="display:flex;align-items:center;margin-bottom:6px;color:#666;font-size:12px;font-family:system-ui,sans-serif;">'
            + '<i class="fa fa-terminal" style="margin-right:5px;color:#2d8cf0;"></i> 安装指令'
            + '<span style="margin-left:auto;font-size:11px;color:#999;">粘贴或点击推荐指令自动填入</span>'
            + '</div>'
            + '<div style="position:relative;">'
            + '<textarea name="install_command" class="layui-textarea" placeholder="输入安装命令，如：winget install Python.Python.3.12" style="min-height:42px;background:#fff;border:1px solid #dce5f0;border-radius:3px;padding:6px 10px;font-family:\'Cascadia Code\',\'Consolas\',monospace;font-size:13px;resize:vertical;line-height:1.5;"></textarea>'
            + '</div>'
            + '<div style="margin-top:8px;display:flex;align-items:center;gap:8px;">'
            + '<button class="layui-btn layui-btn-sm" id="btnCommandInstall" style="background:#2d8cf0;border-color:#2d8cf0;border-radius:3px;"><i class="fa fa-play"></i> 后台执行安装</button>'
            + '<span style="font-size:11px;color:#999;">执行超时: 300s · 退出码会显示在结果面板</span>'
            + '</div>'
            + '</div>'
            + '</div></fieldset>'
            + '</div>';

        layer.open({
            type: 1,
            title: '手动配置 - ' + escapeHtml(title || name),
            area: ['620px', '500px'],
            shade: 0.3,
            content: html,
            btn: ['取消'],
            success: function(layero) {
                // 方式一：手动路径安装
                layero.find('#btnManualInstall').on('click', function() {
                    var p = layero.find('input[name="install_path"]').val();
                    if (!p) {
                        layer.msg('请输入安装路径', {icon: 2, time: 1500});
                        return;
                    }
                    layer.closeAll();
                    doInstall(name, title, p);
                });
                // 方式二：指令后台安装
                layero.find('#btnCommandInstall').on('click', function() {
                    var cmd = layero.find('textarea[name="install_command"]').val();
                    if (!cmd) {
                        layer.msg('请输入安装指令', {icon: 2, time: 1500});
                        return;
                    }
                    layer.closeAll();
                    doInstallByCommand(name, title, cmd);
                });
                // 加载系统推荐指令列表
                loadSuggestCommands(layero, name);
            }
        });
    }

    // 加载系统推荐的安装指令
    function loadSuggestCommands(layero, name) {
        $.ajax({
            url: API + '/extensions/' + encodeURIComponent(name) + '/default-install-command',
            type: 'GET',
            dataType: 'json',
            success: function(res) {
                if (res.code !== 0) return;
                var data = res.data || {};
                var cmds = data.commands || [];
                if (cmds.length === 0) {
                    layero.find('#suggestCommands').html('<div style="color:#999;font-size:12px;padding:4px 0;">暂无可用的推荐安装指令</div>');
                    return;
                }
                var html = '<div style="color:#333;font-size:12px;margin-bottom:6px;font-weight:bold;">推荐指令（点击自动填入）</div>';
                cmds.forEach(function(c) {
                    html += '<div class="suggest-cmd-item" style="padding:4px 8px;margin-bottom:4px;background:#f5f7fa;border:1px solid #e8eaec;border-radius:3px;cursor:pointer;transition:all 0.2s;"'
                        + ' data-cmd="' + escapeHtml(c.command) + '"'
                        + ' title="' + escapeHtml(c.description) + '">'
                        + '<div style="font-size:12px;color:#2d8cf0;">' + escapeHtml(c.title) + '</div>'
                        + '<div style="font-size:11px;color:#999;margin-top:2px;">' + escapeHtml(c.description) + '</div>'
                        + '</div>';
                });
                layero.find('#suggestCommands').html(html);

                // 点击推荐项自动填入文本框
                layero.find('.suggest-cmd-item').on('click', function() {
                    var cmd = $(this).data('cmd');
                    layero.find('textarea[name="install_command"]').val(cmd);
                });
                // 默认选中第一个
                var firstCmd = cmds[0].command;
                if (firstCmd) {
                    layero.find('textarea[name="install_command"]').val(firstCmd);
                }
            },
            error: function() {
                layero.find('#suggestCommands').html('<div style="color:#999;font-size:12px;padding:4px 0;">加载推荐指令失败</div>');
            }
        });
    }

    // 行工具栏事件
    table.on('tool(extTable)', function(obj) {
        var data = obj.data;
        if (obj.event === 'install') {
            layer.confirm('确认安装 ' + (data.title || data.name) + ' 拓展？系统将自动检测本地安装路径。', {
                icon: 3, title: '确认安装'
            }, function(index) {
                layer.close(index);
                doInstall(data.name, data.title);
            });
        } else if (obj.event === 'config') {
            // 弹窗编辑配置
            var html = '<div style="padding:15px;">'
                + '<form class="layui-form" lay-filter="extEditForm">'
                + '<div class="edit-form-item">'
                + '<label class="edit-form-label">路径</label>'
                + '<div class="layui-input-inline" style="width:340px;">'
                + '<input type="text" name="path" class="layui-input" value="' + escapeHtml(data.path || '') + '">'
                + '</div></div>'
                + '<div class="edit-form-item">'
                + '<label class="edit-form-label">配置</label>'
                + '<div class="layui-input-inline" style="width:340px;">'
                + '<textarea name="config" class="layui-textarea" style="min-height:80px;">' + escapeHtml(data.config || '') + '</textarea>'
                + '</div></div>'
                + '<div class="edit-form-item">'
                + '<label class="edit-form-label">状态</label>'
                + '<div class="layui-input-inline" style="width:340px;">'
                + '<input type="checkbox" name="status" lay-skin="switch" lay-text="启用|禁用" ' + (data.status === 1 ? 'checked' : '') + '>'
                + '</div></div>'
                + '</form></div>';

            layer.open({
                type: 1,
                title: '拓展配置 - ' + data.name,
                area: ['520px', '360px'],
                shade: 0.3,
                content: html,
                btn: ['保存', '取消'],
                yes: function(index) {
                    var path = $('input[name="path"]').val();
                    var config = $('textarea[name="config"]').val();
                    var status = $('input[name="status"]').is(':checked') ? 1 : 0;

                    var idx = layer.load(1);
                    $.ajax({
                        url: API + '/extensions/' + encodeURIComponent(data.name),
                        type: 'PUT',
                        dataType: 'json',
                        contentType: 'application/json',
                        data: JSON.stringify({
                            path: path,
                            config: config,
                            status: status
                        }),
                        success: function(res) {
                            layer.close(idx);
                            if (res.code === 0) {
                                layer.msg('保存成功', {icon: 1, time: 1500});
                                layer.close(index);
                                loadData();
                            } else {
                                layer.msg(res.message || '保存失败', {icon: 2, time: 2000});
                            }
                        },
                        error: function() {
                            layer.close(idx);
                            layer.msg('请求失败', {icon: 2, time: 2000});
                        }
                    });
                },
                success: function(layero) {
                    form.render(null, 'extEditForm');
                }
            });
        } else if (obj.event === 'test') {
            // 测试拓展可用性
            var idx = layer.load(1, { content: '正在测试...', shade: [0.3, '#000'] });
            $.ajax({
                url: API + '/extensions/' + encodeURIComponent(data.name) + '/test',
                type: 'POST',
                dataType: 'json',
                success: function(res) {
                    layer.close(idx);
                    if (res.code === 0 && res.data && res.data.available) {
                        layer.open({
                            type: 0,
                            title: '测试结果 - ' + data.name,
                            area: ['400px', '220px'],
                            content: '<div class="test-result-success" style="margin:15px;"><i class="fa fa-check-circle"></i> 可用性测试通过</div>'
                        });
                    } else {
                        var msg = res.message || (res.data ? JSON.stringify(res.data) : '测试失败');
                        layer.open({
                            type: 0,
                            title: '测试结果 - ' + data.name,
                            area: ['400px', '220px'],
                            content: '<div class="test-result-fail" style="margin:15px;"><i class="fa fa-times-circle"></i> ' + escapeHtml(msg) + '</div>'
                        });
                    }
                },
                error: function(xhr) {
                    layer.close(idx);
                    var msg = '请求失败';
                    try {
                        var r = JSON.parse(xhr.responseText);
                        msg = r.message || msg;
                    } catch(e) {}
                    layer.open({
                        type: 0,
                        title: '测试结果 - ' + data.name,
                        area: ['400px', '200px'],
                        content: '<div class="test-result-fail" style="margin:15px;"><i class="fa fa-times-circle"></i> ' + escapeHtml(msg) + '</div>'
                    });
                }
            });
        }
    });

    function escapeHtml(str) {
        if (str === null || str === undefined) return '';
        return String(str)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    loadData();
});
</script>
</body>
</html>
