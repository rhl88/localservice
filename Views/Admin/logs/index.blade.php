<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>日志 - 本地服务</title>
    <link rel="stylesheet" href="{{ asset('CmsProUi/component/pear/css/pear.css') }}">
    <link rel="stylesheet" href="{{ asset('CmsProUi/font-awesome/4.7.0/css/font-awesome.min.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/admin.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/variables.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/reset.css') }}">
</head>
<body>
<div class="pear-container">
    <div class="layui-card">
        <div class="layui-card-body">
            <form class="layui-form" lay-filter="logFilter">
                <div class="layui-form-item">
                    <div class="layui-inline">
                        <label class="layui-form-label">应用标识</label>
                        <div class="layui-input-inline" style="width:140px;">
                            <input type="text" name="app_id" id="searchAppId" placeholder="app_id" class="layui-input" autocomplete="off">
                        </div>
                    </div>
                    <div class="layui-inline">
                        <label class="layui-form-label">级别</label>
                        <div class="layui-input-inline" style="width:120px;">
                            <select name="level" id="searchLevel">
                                <option value="">全部</option>
                                <option value="debug">DEBUG</option>
                                <option value="info">INFO</option>
                                <option value="warn">WARN</option>
                                <option value="error">ERROR</option>
                            </select>
                        </div>
                    </div>
                    <div class="layui-inline">
                        <button class="layui-btn layui-btn-sm" lay-submit lay-filter="btnSearch"><i class="fa fa-search"></i> 查询</button>
                        <button type="reset" class="layui-btn layui-btn-sm layui-btn-primary"><i class="fa fa-refresh"></i> 重置</button>
                    </div>
                    <div class="layui-inline" style="float:right;">
                        <button type="button" class="layui-btn layui-btn-sm layui-btn-danger" id="btnClearLogs"><i class="fa fa-trash"></i> 清空</button>
                    </div>
                    <div class="layui-inline" style="float:right;">
                        <input type="checkbox" id="autoRefresh" lay-skin="switch" lay-text="自动刷新|手动" lay-filter="autoRefresh" value="1">
                        <label style="font-size:13px;color:#666;margin-left:4px;">自动刷新(5秒)</label>
                    </div>
                </div>
            </form>
        </div>
    </div>

    <div class="layui-card">
        <div class="layui-card-body">
            <table id="logTable" lay-filter="logTable"></table>
        </div>
    </div>
</div>

<script type="text/html" id="levelTpl">
@verbatim
{{# var cls = { 'error': 'layui-bg-red', 'warn': 'layui-bg-orange', 'info': 'layui-bg-blue', 'debug': 'layui-bg-gray' }; }}
<span class="layui-badge {{ cls[d.level] || 'layui-bg-gray' }}">{{ (d.level || '').toUpperCase() }}</span>
@endverbatim
</script>

<script src="{{ asset('CmsProUi/component/layui/layui.js') }}"></script>
<script src="{{ asset('CmsProUi/component/pear/pear.js') }}"></script>
<script>
layui.use(['table', 'form', 'layer', 'jquery'], function(){
    var table = layui.table, form = layui.form, layer = layui.layer, $ = layui.jquery;
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

    form.render();

    var currentPage = 1;
    var pollTimer = null;
    var appMap = {};

    // 将 ISO 8601 或任意日期字符串统一格式化为 Y-m-d H:i:s
    function formatTime(t) {
        if (!t || t === '-' || t === 'null') return '-';
        if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(t)) return t;
        try {
            var d = new Date(t);
            if (isNaN(d.getTime())) return t;
            var pad = function(n) { return n < 10 ? '0' + n : String(n); };
            return d.getFullYear() + '-' + pad(d.getMonth()+1) + '-' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
        } catch(e) { return t; }
    }

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

    function loadData(page) {
        currentPage = page || 1;
        var params = {
            page: currentPage,
            page_size: 20,
            app_id: $('#searchAppId').val(),
            level: $('#searchLevel').val()
        };
        $.ajax({
            url: API + '/logs',
            type: 'GET',
            dataType: 'json',
            data: params,
            success: function(res) {
                if (res.code !== 0) {
                    layer.msg(res.message || '加载失败', {icon: 2, time: 2000});
                    return;
                }
                var d = res.data;
                table.render({
                    elem: '#logTable',
                    data: d.items || [],
                    cols: [[
                        {field: 'id', title: 'ID', width: 70},
                        {field: 'app_id', title: '应用', width: 130, templet: function(d) { return appMap[d.app_id] || d.app_id; }},
                        {field: 'level', title: '级别', width: 90, templet: '#levelTpl'},
                        {field: 'message', title: '消息', minWidth: 300},
                        {field: 'task_id', title: '任务ID', width: 180, templet: function(d) { return d.task_id || '-'; }},
                        {field: 'create_time', title: '时间', width: 175, templet: function(d) { return formatTime(d.create_time); }}
                    ]],
                    page: {
                        count: d.total || 0,
                        curr: d.page || currentPage,
                        limit: d.page_size || 20
                    }
                });
            },
            error: function() {
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    }

    form.on('submit(btnSearch)', function() {
        loadData(1);
        return false;
    });

    table.on('page(logTable)', function(obj) {
        loadData(obj.curr);
    });

    // 清空日志
    $('#btnClearLogs').click(function() {
        layer.confirm('确认清空全部日志？此操作不可恢复。', {icon: 3, title: '确认清空'}, function(index) {
            var idx = layer.load(1);
            $.ajax({
                url: API + '/logs/clear',
                type: 'POST',
                dataType: 'json',
                success: function(res) {
                    layer.close(idx);
                    if (res.code === 0) {
                        layer.msg('日志已清空', {icon: 1, time: 1500});
                        loadData(1);
                    } else {
                        layer.msg(res.message || '清空失败', {icon: 2, time: 2000});
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

    // 自动刷新开关
    form.on('switch(autoRefresh)', function(obj) {
        if (obj.elem.checked) {
            if (pollTimer) clearInterval(pollTimer);
            pollTimer = setInterval(function() {
                loadData(currentPage);
            }, 5000);
            layer.msg('自动刷新已开启（5秒间隔）', {icon: 1, time: 1500});
        } else {
            if (pollTimer) {
                clearInterval(pollTimer);
                pollTimer = null;
            }
            layer.msg('自动刷新已关闭', {icon: 0, time: 1500});
        }
    });

    // 初始加载
    loadData(1);
});
</script>
</body>
</html>
