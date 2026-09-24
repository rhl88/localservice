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
            <div class="card-header"><i class="fa fa-server"></i> 服务状态
                <span id="updateBtnWrap" style="margin-left:auto;display:none;">
                    <button class="layui-btn layui-btn-sm layui-btn-warm" id="btnUpdate" title=""><i class="fa fa-arrow-circle-up"></i> <span id="updateBtnText">更新</span></button>
                </span>
            </div>
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

    // ============ 本地服务在线更新 ============
    var updateInfo = null;

    // 检查更新：有新版本时在「服务状态」行右侧显示更新按钮
    function checkUpdate() {
        $('#updateBtnWrap').hide();
        $.ajax({
            url: API + '/service/update/check',
            type: 'GET',
            dataType: 'json',
            success: function(res) {
                if (res.code === 0 && res.data && res.data.has_update) {
                    updateInfo = res.data;
                    $('#updateBtnText').text('更新到 ' + res.data.latest_version);
                    $('#btnUpdate').attr('title', (res.data.changelog || '').substring(0, 200));
                    $('#updateBtnWrap').show();
                }
            }
            // 检查失败静默：版本服务器不可达/服务离线时不打扰用户
        });
    }

    // 发起更新（分段状态机：start 创建任务 → 循环 step 推进 → finished/failed 终态）
    var updateRunning = false;
    var updLayerIdx = null;

    // 字节数格式化
    function fmtBytes(n) {
        n = parseInt(n, 10);
        if (isNaN(n) || n < 0) return '-';
        if (n < 1024) return n + ' B';
        if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
        return (n / 1024 / 1024).toFixed(2) + ' MB';
    }

    // 关闭更新弹窗
    function closeUpdateDialog() {
        if (updLayerIdx !== null) {
            layer.close(updLayerIdx);
            updLayerIdx = null;
        }
    }

    // 步骤节点（下载 → 替换 → 验证 → 完成）
    function updStepNode(id, text, seq) {
        return '<div class="upd-step" id="' + id + '">'
            + '<div class="upd-step-dot">' + seq + '</div>'
            + '<div class="upd-step-text">' + text + '</div></div>';
    }

    // 打开更新进度弹窗（步骤条 + 进度条 + 状态动画）
    function openUpdateDialog() {
        var html = '<style>'
            + '@keyframes updSlide{0%{left:-40%}100%{left:100%}}'
            + '.upd-steps{display:flex;align-items:center;margin-bottom:20px;}'
            + '.upd-step{display:flex;flex-direction:column;align-items:center;gap:5px;min-width:46px;}'
            + '.upd-step-dot{width:22px;height:22px;border-radius:50%;background:#eef1f6;color:#bbb;display:flex;align-items:center;justify-content:center;font-size:11px;font-weight:600;transition:all .3s;}'
            + '.upd-step.active .upd-step-dot{background:#1e9fff;color:#fff;box-shadow:0 0 0 4px rgba(30,159,255,.15);}'
            + '.upd-step.done .upd-step-dot{background:#52c41a;color:#fff;}'
            + '.upd-step-text{font-size:12px;color:#999;white-space:nowrap;}'
            + '.upd-step.active .upd-step-text{color:#1e9fff;font-weight:600;}'
            + '.upd-step.done .upd-step-text{color:#52c41a;}'
            + '.upd-step-line{flex:1;height:2px;background:#eef1f6;margin:0 8px 18px;transition:background .3s;}'
            + '.upd-step-line.done{background:#52c41a;}'
            + '</style>'
            + '<div style="padding:20px 24px 18px;">'
            + '<div class="upd-steps">'
            + updStepNode('updStepDownload', '下载', '1')
            + '<div class="upd-step-line"></div>'
            + updStepNode('updStepReplace', '替换', '2')
            + '<div class="upd-step-line"></div>'
            + updStepNode('updStepVerify', '验证', '3')
            + '<div class="upd-step-line"></div>'
            + updStepNode('updStepDone', '完成', '<i class="fa fa-check"></i>')
            + '</div>'
            + '<div style="background:#eef1f6;border-radius:5px;height:10px;overflow:hidden;position:relative;">'
            + '<div id="updBar" style="width:0%;height:100%;background:linear-gradient(90deg,#1e9fff,#4da8ff);border-radius:5px;transition:width .4s ease;"></div>'
            + '<div id="updBarAnim" style="display:none;position:absolute;top:0;left:-40%;width:40%;height:100%;background:linear-gradient(90deg,transparent,rgba(30,159,255,.35),transparent);animation:updSlide 1.2s infinite linear;"></div>'
            + '</div>'
            + '<div style="display:flex;justify-content:space-between;align-items:center;margin-top:10px;font-size:13px;color:#666;">'
            + '<span id="updStatus"><i class="fa fa-spinner fa-spin" style="color:#1e9fff;"></i> 准备下载...</span>'
            + '<span id="updPercent" style="font-weight:600;color:#1e9fff;"></span>'
            + '</div>'
            + '<div id="updDetail" style="margin-top:4px;font-size:12px;color:#aaa;text-align:right;"></div>'
            + '<div id="updError" style="display:none;margin-top:12px;padding:10px 12px;background:#fff2f0;border:1px solid #ffccc7;border-radius:4px;color:#ff4d4f;font-size:12px;line-height:1.7;"></div>'
            + '</div>';
        updLayerIdx = layer.open({
            type: 1,
            title: '<i class="fa fa-cloud-download" style="color:#1e9fff;"></i> 更新本地服务',
            area: '440px',
            content: html,
            shade: [0.3, '#000'],
            closeBtn: 2
        });
    }

    // 按步骤状态渲染弹窗（对已关闭弹窗操作为 jQuery 空集，无害）
    function renderUpdateUI(d) {
        var order = ['download', 'replace', 'verify'];
        var nodeIds = ['updStepDownload', 'updStepReplace', 'updStepVerify'];
        var cur = order.indexOf(d.step);
        var lines = $('.upd-step-line');

        $.each(nodeIds, function(i, id) {
            var $dot = $('#' + id);
            $dot.removeClass('active done');
            if (i < cur) {
                $dot.addClass('done').find('.upd-step-dot').html('<i class="fa fa-check"></i>');
            } else if (i === cur) {
                $dot.addClass('active');
            }
        });
        lines.removeClass('done');
        for (var i = 0; i < cur; i++) {
            lines.eq(i).addClass('done');
        }
        var $doneNode = $('#updStepDone');
        $doneNode.removeClass('active done');
        if (d.step === 'finished') {
            $doneNode.addClass('done');
        }

        var $bar = $('#updBar'), $anim = $('#updBarAnim'), $pct = $('#updPercent');
        var $status = $('#updStatus'), $detail = $('#updDetail'), $err = $('#updError');

        if (d.step === 'download') {
            var p = d.progress;
            $bar.css('background', 'linear-gradient(90deg,#1e9fff,#4da8ff)').css('width', (p !== null ? p : 0) + '%');
            $anim.show();
            $pct.text(p !== null ? p + '%' : '');
            $status.html('<i class="fa fa-spinner fa-spin" style="color:#1e9fff;"></i> 正在下载新版本 v' + d.version + '...');
            $detail.text(fmtBytes(d.downloaded_bytes) + ' / ' + (d.expected_size > 0 ? fmtBytes(d.expected_size) : '未知大小'));
        } else if (d.step === 'replace') {
            $bar.css('background', 'linear-gradient(90deg,#faad14,#ffc53d)').css('width', '100%');
            $anim.show();
            $pct.text('');
            $status.html('<i class="fa fa-refresh fa-spin" style="color:#faad14;"></i> 正在替换服务文件，服务将短暂停止...');
            $detail.text('');
        } else if (d.step === 'verify') {
            $bar.css('background', 'linear-gradient(90deg,#52c41a,#73d13d)').css('width', '100%');
            $anim.hide();
            $pct.text('');
            $status.html('<i class="fa fa-heartbeat" style="color:#52c41a;"></i> 正在验证新版本启动（第 ' + d.verify_round + '/5 轮）...');
            $detail.text('');
        } else if (d.step === 'finished') {
            $bar.css('background', '#52c41a').css('width', '100%');
            $anim.hide();
            $pct.text('100%').css('color', '#52c41a');
            $status.html('<i class="fa fa-check-circle" style="color:#52c41a;"></i> ' + (d.message || '更新完成'));
            $detail.text('');
        } else if (d.step === 'failed') {
            $bar.css('background', '#ff4d4f').css('width', '100%');
            $anim.hide();
            $pct.text('失败').css('color', '#ff4d4f');
            $status.html('<i class="fa fa-times-circle" style="color:#ff4d4f;"></i> 更新失败');
            $detail.text('');
            $err.show().html((d.message || '未知错误') + '<br><span style="color:#999;">可关闭此窗口，稍后在仪表盘重新发起更新。</span>');
        }
    }

    // 循环驱动更新步骤直至终态
    function runUpdateSteps(runId) {
        openUpdateDialog();
        var nextStep = function() {
            $.ajax({
                url: API + '/service/update/step',
                type: 'POST',
                dataType: 'json',
                data: { run_id: runId },
                timeout: 60000,
                success: function(res) {
                    var d = res.data;
                    // 无状态数据的异常响应：直接终止
                    if (!d || !d.step) {
                        updateRunning = false;
                        closeUpdateDialog();
                        layer.msg(res.message || '更新失败', {icon: 2, time: 8000});
                        return;
                    }
                    renderUpdateUI(d);
                    if (d.step === 'finished') {
                        updateRunning = false;
                        $('#updateBtnWrap').hide();
                        updateInfo = null;
                        // 弹窗展示完成态 2 秒后自动关闭并刷新状态
                        setTimeout(function() {
                            closeUpdateDialog();
                            layer.msg(d.message || '更新完成', {icon: 1, time: 2500});
                            loadStatus();
                            checkUpdate();
                        }, 2000);
                        return;
                    }
                    if (d.step === 'failed') {
                        updateRunning = false;
                        return;
                    }
                    // 未到终态：继续推进
                    setTimeout(nextStep, 600);
                },
                error: function(xhr) {
                    updateRunning = false;
                    closeUpdateDialog();
                    var msg = '更新请求失败';
                    try { msg = JSON.parse(xhr.responseText).message || msg; } catch(e) {}
                    layer.msg(msg, {icon: 2, time: 8000});
                }
            });
        };
        nextStep();
    }

    $('#btnUpdate').click(function() {
        if (!updateInfo || updateRunning) return;
        var html = '<div style="padding:10px 15px;line-height:1.8;">'
            + '将把本地服务从 <b>' + updateInfo.current_version + '</b> 升级到 <b>' + updateInfo.latest_version + '</b>。'
            + '<br>更新过程中服务会短暂停止并自动重启。'
            + (updateInfo.changelog ? '<br><br><div style="background:#f8f8f8;border-radius:4px;padding:8px 10px;font-size:12px;color:#666;max-height:120px;overflow:auto;">' + updateInfo.changelog + '</div>' : '')
            + '</div>';
        layer.confirm(html, {icon: 3, title: '确认更新本地服务', area: '460px'}, function(index) {
            layer.close(index);
            updateRunning = true;
            var loadIdx = layer.load(1, { content: '正在发起更新...' });
            $.ajax({
                url: API + '/service/update/start',
                type: 'POST',
                dataType: 'json',
                timeout: 30000,
                success: function(res) {
                    layer.close(loadIdx);
                    if (res.code !== 0 || !res.data || !res.data.run_id) {
                        updateRunning = false;
                        layer.msg(res.message || '发起更新失败', {icon: 2, time: 5000});
                        return;
                    }
                    runUpdateSteps(res.data.run_id);
                },
                error: function(xhr) {
                    layer.close(loadIdx);
                    updateRunning = false;
                    var msg = '发起更新失败';
                    try { msg = JSON.parse(xhr.responseText).message || msg; } catch(e) {}
                    layer.msg(msg, {icon: 2, time: 8000});
                }
            });
        });
    });

    // 页面加载时恢复进行中的更新任务（刷新不中断）
    $.ajax({
        url: API + '/service/update/status',
        type: 'GET',
        dataType: 'json',
        success: function(res) {
            if (res.code === 0 && res.data && ['download', 'replace', 'verify'].indexOf(res.data.step) !== -1) {
                updateRunning = true;
                runUpdateSteps(res.data.run_id);
            }
        }
    });

    // 初始化
    loadStatus();
    loadRecentTasks();
    startPoll();
    checkUpdate();
});
</script>
</body>
</html>
