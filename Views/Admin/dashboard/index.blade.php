<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>本地服务仪表盘</title>
    <link rel="stylesheet" href="{{ asset('CmsProUi/component/pear/css/pear.css') }}">
    <link rel="stylesheet" href="{{ asset('CmsProUi/font-awesome/4.7.0/css/font-awesome.min.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/admin.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/variables.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/reset.css') }}">
    <style>
        .dashboard-container { padding: 15px; display: flex; gap: 15px; flex-wrap: wrap; }
        .card { background: #fff; border-radius: 6px; box-shadow: 0 1px 4px rgba(0,0,0,0.06); border: 1px solid #ebeef5; }
        .card-header { padding: 14px 18px; border-bottom: 1px solid #ebeef5; font-size: 15px; font-weight: 600; color: #333; display: flex; align-items: center; gap: 8px; }
        .card-body { padding: 18px; }
        .card-left { flex: 0 0 380px; }
        .card-right { flex: 1; min-width: 300px; }
        .card-full { width: 100%; }
        .status-indicator { display: inline-flex; align-items: center; gap: 6px; }
        .status-dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
        .status-dot.running { background: #52c41a; box-shadow: 0 0 6px rgba(82,196,26,0.5); }
        .status-dot.stopped { background: #ff4d4f; box-shadow: 0 0 6px rgba(255,77,79,0.5); }
        .status-text { font-size: 16px; font-weight: 600; }
        .status-text.running { color: #52c41a; }
        .status-text.stopped { color: #ff4d4f; }
        .version-info { margin-top: 10px; font-size: 13px; color: #666; }
        .version-info span { color: #333; font-weight: 500; }
        .quick-actions { margin-top: 16px; display: flex; flex-wrap: wrap; gap: 8px; }
        .quick-actions .layui-btn { margin: 0; }
        .stat-row { display: flex; gap: 15px; flex-wrap: wrap; }
        .stat-item { flex: 1; min-width: 100px; text-align: center; padding: 15px 10px; background: #fafbfc; border-radius: 6px; border: 1px solid #f0f0f0; }
        .stat-item .stat-value { font-size: 28px; font-weight: 700; color: #1e9fff; }
        .stat-item .stat-label { font-size: 13px; color: #999; margin-top: 4px; }
        .stat-item .stat-value.green { color: #52c41a; }
        .stat-item .stat-value.orange { color: #faad14; }
        .stat-item .stat-value.blue { color: #1e9fff; }
        .path-tip { margin-top: 10px; padding: 8px 12px; background: #fff7e6; border: 1px solid #ffd591; border-radius: 4px; font-size: 12px; color: #d46b08; line-height: 1.6; display: none; }
    </style>
</head>
<body>
<div class="pear-container">
    <div class="dashboard-container">
        <!-- 左侧：服务状态卡片 -->
        <div class="card card-left">
            <div class="card-header"><i class="fa fa-server"></i> 服务状态</div>
            <div class="card-body">
                <div class="status-indicator">
                    <span class="status-dot" id="statusDot"></span>
                    <span class="status-text" id="statusText">检测中...</span>
                </div>
                <div class="version-info">
                    版本：<span id="versionText">-</span>
                </div>
                <div class="quick-actions">
                    <button class="layui-btn layui-btn-sm layui-btn-primary" id="btnRestart"><i class="fa fa-repeat"></i> 重启</button>
                    <button class="layui-btn layui-btn-sm layui-btn-primary" id="btnStart"><i class="fa fa-play"></i> 启动</button>
                    <button class="layui-btn layui-btn-sm layui-btn-primary" id="btnStop"><i class="fa fa-stop"></i> 停止</button>
                    <button class="layui-btn layui-btn-sm" id="btnRefresh"><i class="fa fa-refresh"></i> 刷新</button>
                </div>
                <div class="path-tip" id="pathTip"></div>
            </div>
        </div>

        <!-- 右侧：统计卡片 -->
        <div class="card card-right">
            <div class="card-header"><i class="fa fa-bar-chart"></i> 任务统计</div>
            <div class="card-body">
                <div class="stat-row">
                    <div class="stat-item">
                        <div class="stat-value blue" id="statsTotal">-</div>
                        <div class="stat-label">任务总数</div>
                    </div>
                    <div class="stat-item">
                        <div class="stat-value green" id="statsSuccess">-</div>
                        <div class="stat-label">成功</div>
                    </div>
                    <div class="stat-item">
                        <div class="stat-value orange" id="statsRate">-</div>
                        <div class="stat-label">成功率</div>
                    </div>
                </div>
            </div>
        </div>

        <!-- 底部：最近任务表格 -->
        <div class="card card-full">
            <div class="card-header"><i class="fa fa-list-alt"></i> 最近任务</div>
            <div class="card-body" style="padding:0;">
                <table id="recentTable" lay-filter="recentTable"></table>
            </div>
        </div>
    </div>
</div>

<script type="text/html" id="taskStatusTpl">
@verbatim
{{# if(d.status === 'pending'){ }}<span class="layui-badge layui-bg-orange">待执行</span>{{# } }}
{{# if(d.status === 'running'){ }}<span class="layui-badge layui-bg-blue">运行中</span>{{# } }}
{{# if(d.status === 'success'){ }}<span class="layui-badge layui-bg-green">成功</span>{{# } }}
{{# if(d.status === 'failed'){ }}<span class="layui-badge layui-bg-red">失败</span>{{# } }}
{{# if(d.status === 'cancelled'){ }}<span class="layui-badge layui-bg-gray">已取消</span>{{# } }}
@endverbatim
</script>

<script src="{{ asset('CmsProUi/component/layui/layui.js') }}"></script>
<script src="{{ asset('CmsProUi/component/pear/pear.js') }}"></script>
<script>
layui.use(['table', 'layer', 'jquery'], function(){
    var table = layui.table, layer = layui.layer, $ = layui.jquery;
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

    var pollTimer = null;
    var currentStatus = null;
    var appMap = {};

    // 加载已安装应用映射
    $.ajax({
        url: API + '/service/installed-apps',
        type: 'GET',
        dataType: 'json',
        async: false,
        success: function(res) {
            if (res.code === 0 && res.data) {
                $.each(res.data, function(i, app) {
                    appMap[app.app_id] = app.name;
                });
            }
        }
    });

    // 将 ISO 8601 或任意日期字符串统一格式化为 Y-m-d H:i:s
    function formatTime(t) {
        if (!t || t === '-' || t === 'null') return '-';
        // 已经是 Y-m-d H:i:s 格式则直接返回
        if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(t)) return t;
        // ISO 8601 格式转换
        try {
            var d = new Date(t);
            if (isNaN(d.getTime())) return t;
            var pad = function(n) { return n < 10 ? '0' + n : String(n); };
            return d.getFullYear() + '-' + pad(d.getMonth()+1) + '-' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
        } catch(e) { return t; }
    }

    // 加载服务状态和统计
    function loadStatus() {
        $.ajax({
            url: API + '/service/status',
            type: 'GET',
            dataType: 'json',
            success: function(res) {
                if (res.code !== 0) {
                    layer.msg(res.message || '获取状态失败', {icon: 2, time: 2000});
                    return;
                }
                var d = res.data;
                var svc = d.service;

                var online = svc.online;
                currentStatus = svc;

                var dot = $('#statusDot');
                var txt = $('#statusText');
                if (online) {
                    dot.removeClass('stopped').addClass('running');
                    txt.removeClass('stopped').addClass('running').text('运行中');
                    $('#btnStart').prop('disabled', true).addClass('layui-btn-disabled');
                    $('#btnRestart').prop('disabled', false).removeClass('layui-btn-disabled');
                    $('#btnStop').prop('disabled', false).removeClass('layui-btn-disabled');
                } else {
                    dot.removeClass('running').addClass('stopped');
                    txt.removeClass('running').addClass('stopped').text('已停止');
                    $('#btnStart').prop('disabled', false).removeClass('layui-btn-disabled');
                    $('#btnRestart').prop('disabled', true).addClass('layui-btn-disabled');
                    $('#btnStop').prop('disabled', true).addClass('layui-btn-disabled');
                }
                $('#versionText').text(svc.version || '-');

                if (d.stats) {
                    $('#statsTotal').text(d.stats.total || 0);
                    $('#statsSuccess').text(d.stats.success || 0);
                    $('#statsRate').text((d.stats.success_rate || 0) + '%');
                }
            },
            error: function() {
                layer.msg('请求服务状态失败', {icon: 2, time: 2000});
            }
        });
    }

    // 加载最近任务表格
    function loadRecentTasks() {
        $.ajax({
            url: API + '/tasks',
            type: 'GET',
            dataType: 'json',
            data: { page: 1, page_size: 5 },
            success: function(res) {
                if (res.code !== 0) {
                    layer.msg(res.message || '获取最近任务失败', {icon: 2, time: 2000});
                    return;
                }
                var d = res.data;
                var items = d.items || [];
                table.render({
                    elem: '#recentTable',
                    data: items,
                    cols: [[
                        {field: 'id', title: 'ID'},
                        {field: 'app_id', title: '应用', templet: function(d) { return appMap[d.app_id] || d.app_id; }},
                        {field: 'type', title: '类型'},
                        {field: 'mode', title: '模式'},
                        {field: 'status', title: '状态', templet: '#taskStatusTpl'},
                        {field: 'duration_ms', title: '耗时(ms)'},
                        {field: 'create_time', title: '创建时间', templet: function(d) { return formatTime(d.create_time); }}
                    ]],
                    page: false,
                    limit: 5
                });
            },
            error: function() {
                layer.msg('获取最近任务失败', {icon: 2, time: 2000});
            }
        });
    }

    // 启动轮询
    function startPoll() {
        if (pollTimer) clearInterval(pollTimer);
        pollTimer = setInterval(loadStatus, 10000);
    }

    // 启动服务
    $('#btnStart').click(function() {
        var idx = layer.load(1, { content: '正在启动服务...', shade: [0.3, '#000'] });
        $.ajax({
            url: API + '/service/start',
            type: 'POST',
            dataType: 'json',
            success: function(res) {
                layer.close(idx);
                if (res.code === 0) {
                    layer.msg('启动命令已发送，请稍候刷新查看状态', {icon: 1, time: 2000});
                    setTimeout(loadStatus, 2000);
                } else if (res.code === 403) {
                    // 权限不足，引导用户手动操作
                    $('#pathTip').show().html(
                        '<i class="fa fa-exclamation-triangle"></i> '
                        + res.message
                        + '。<br>可前往 <a href="/admin/cmspro/localservice/settings" style="color:#1e9fff;text-decoration:underline;">设置页面</a> 生成安装脚本后以 root 权限执行。'
                    );
                } else {
                    layer.msg(res.message || '启动失败', {icon: 2, time: 3000});
                }
            },
            error: function() {
                layer.close(idx);
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    });

    // 停止服务
    $('#btnStop').click(function() {
        layer.confirm('确认停止本地服务？', {icon: 3, title: '确认操作'}, function(index) {
            var idx = layer.load(1, { content: '正在停止服务...', shade: [0.3, '#000'] });
            $.ajax({
                url: API + '/service/stop',
                type: 'POST',
                dataType: 'json',
                success: function(res) {
                    layer.close(idx);
                    if (res.code === 0) {
                        layer.msg('停止命令已发送', {icon: 1, time: 2000});
                        setTimeout(loadStatus, 2000);
                    } else if (res.code === 403) {
                        $('#pathTip').show().html(
                            '<i class="fa fa-exclamation-triangle"></i> '
                            + res.message
                            + '。<br>可前往 <a href="/admin/cmspro/localservice/settings" style="color:#1e9fff;text-decoration:underline;">设置页面</a> 生成安装脚本后以 root 权限执行。'
                        );
                    } else {
                        layer.msg(res.message || '停止失败', {icon: 2, time: 3000});
                    }
                },
                error: function() {
                    layer.close(idx);
                    layer.msg('请求失败', {icon: 2, time: 2000});
                }
            });
            layer.close(index);
        });
    });

    // 重启服务
    $('#btnRestart').click(function() {
        layer.confirm('确认重启本地服务？', {icon: 3, title: '确认操作'}, function(index) {
            var idx = layer.load(1, { content: '正在重启服务...', shade: [0.3, '#000'] });
            $.ajax({
                url: API + '/service/restart',
                type: 'POST',
                dataType: 'json',
                success: function(res) {
                    layer.close(idx);
                    if (res.code === 0) {
                        layer.msg('重启命令已发送，请稍候刷新查看状态', {icon: 1, time: 2000});
                        setTimeout(loadStatus, 3000);
                    } else if (res.code === 403) {
                        $('#pathTip').show().html(
                            '<i class="fa fa-exclamation-triangle"></i> '
                            + res.message
                            + '。<br>可前往 <a href="/admin/cmspro/localservice/settings" style="color:#1e9fff;text-decoration:underline;">设置页面</a> 生成安装脚本后以 root 权限执行。'
                        );
                    } else {
                        layer.msg(res.message || '重启失败', {icon: 2, time: 3000});
                    }
                },
                error: function() {
                    layer.close(idx);
                    layer.msg('请求失败', {icon: 2, time: 2000});
                }
            });
            layer.close(index);
        });
    });

    // 刷新
    $('#btnRefresh').click(function() {
        loadStatus();
        loadRecentTasks();
        layer.msg('已刷新', {icon: 1, time: 1000});
    });

    // 初始化
    loadStatus();
    loadRecentTasks();
    startPoll();
});
</script>
</body>
</html>
