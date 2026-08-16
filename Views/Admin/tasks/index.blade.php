<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>任务记录 - 本地服务</title>
    <link rel="stylesheet" href="{{ asset('CmsProUi/component/pear/css/pear.css') }}">
    <link rel="stylesheet" href="{{ asset('CmsProUi/font-awesome/4.7.0/css/font-awesome.min.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/admin.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/variables.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/reset.css') }}">
    <style>
        .task-detail-table td:first-child { width: 120px; font-weight: 500; color: #555; background: #fafbfc; }
        .task-detail-table td { padding: 8px 12px; }
        .task-detail-table pre { margin: 0; max-height: 200px; overflow: auto; border-radius: 2px; font-size: 12px; }
        .json-viewer { max-height: 250px; overflow: auto; background: #1e1e1e; border-radius: 4px; padding: 12px; font-family: 'Cascadia Code', 'Consolas', monospace; font-size: 12px; line-height: 1.6; white-space: pre-wrap; word-break: break-all; }
        .json-key { color: #9cdcfe; }
        .json-string { color: #ce9178; }
        .json-number { color: #b5cea8; }
        .json-boolean { color: #569cd6; }
        .json-null { color: #569cd6; }
        .json-brace { color: #d4d4d4; }
    </style>
</head>
<body>
<div class="pear-container">
    <div class="layui-card">
        <div class="layui-card-body">
            <form class="layui-form" lay-filter="taskFilter">
                <div class="layui-form-item">
                    <div class="layui-inline">
                        <label class="layui-form-label">应用标识</label>
                        <div class="layui-input-inline" style="width:140px;">
                            <input type="text" name="app_id" id="searchAppId" placeholder="app_id" class="layui-input" autocomplete="off">
                        </div>
                    </div>
                    <div class="layui-inline">
                        <label class="layui-form-label">状态</label>
                        <div class="layui-input-inline" style="width:130px;">
                            <select name="status" id="searchStatus">
                                <option value="">全部</option>
                                <option value="pending">待执行</option>
                                <option value="running">运行中</option>
                                <option value="success">成功</option>
                                <option value="failed">失败</option>
                                <option value="cancelled">已取消</option>
                            </select>
                        </div>
                    </div>
                    <div class="layui-inline">
                        <button class="layui-btn layui-btn-sm" lay-submit lay-filter="btnSearch"><i class="fa fa-search"></i> 查询</button>
                        <button type="reset" class="layui-btn layui-btn-sm layui-btn-primary"><i class="fa fa-refresh"></i> 重置</button>
                    </div>
                    <div class="layui-inline" style="float:right;">
                        <button type="button" class="layui-btn layui-btn-sm layui-btn-danger" id="btnClearTasks"><i class="fa fa-trash"></i> 清空</button>
                    </div>
            </form>
        </div>
    </div>

    <div class="layui-card">
        <div class="layui-card-body">
            <table id="taskTable" lay-filter="taskTable"></table>
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

<script type="text/html" id="taskBar">
@verbatim
<a class="layui-btn layui-btn-xs layui-btn-normal" lay-event="detail">详情</a>
{{# if(d.status === 'running' || d.status === 'pending'){ }}
<a class="layui-btn layui-btn-xs layui-btn-danger" lay-event="cancel">取消</a>
{{# } }}
@endverbatim
</script>

<script type="text/html" id="durationTpl">
@verbatim
{{ d.duration_ms != null ? d.duration_ms + ' ms' : '-' }}
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

    // 渲染下拉框
    form.render();

    var currentPage = 1;
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
            status: $('#searchStatus').val()
        };
        $.ajax({
            url: API + '/tasks',
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
                    elem: '#taskTable',
                    data: d.items || [],
                    cols: [[
                        {field: 'id', title: 'ID', width: 200},
                        {field: 'app_id', title: '应用', width: 130, templet: function(d) { return appMap[d.app_id] || d.app_id; }},
                        {field: 'type', title: '类型', width: 120},
                        {field: 'mode', title: '模式', width: 80},
                        {field: 'status', title: '状态', width: 100, templet: '#taskStatusTpl'},
                        {field: 'duration_ms', title: '耗时', width: 100, templet: '#durationTpl'},
                        {field: 'create_time', title: '创建时间', minWidth: 170, templet: function(d) { return formatTime(d.create_time); }},
                        {title: '操作', width: 160, toolbar: '#taskBar', fixed: 'right'}
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

    table.on('page(taskTable)', function(obj) {
        loadData(obj.curr);
    });

    // 行工具栏事件
    table.on('tool(taskTable)', function(obj) {
        var data = obj.data;
        if (obj.event === 'detail') {
            // 查看任务详情
            var idx = layer.load(1);
            $.ajax({
                url: API + '/tasks/' + encodeURIComponent(data.id),
                type: 'GET',
                dataType: 'json',
                success: function(res) {
                    layer.close(idx);
                    if (res.code !== 0) {
                        layer.msg(res.message || '获取详情失败', {icon: 2, time: 2000});
                        return;
                    }
                    var d = res.data;
                    var statusMap = {
                        'pending': '<span class="layui-badge layui-bg-orange">待执行</span>',
                        'running': '<span class="layui-badge layui-bg-blue">运行中</span>',
                        'success': '<span class="layui-badge layui-bg-green">成功</span>',
                        'failed': '<span class="layui-badge layui-bg-red">失败</span>',
                        'cancelled': '<span class="layui-badge layui-bg-gray">已取消</span>'
                    };
                    var html = '<div style="padding:15px;">'
                        + '<table class="layui-table task-detail-table" lay-size="sm" style="margin:0;">'
                        + '<tr><td>任务ID</td><td>' + escapeHtml(d.id) + '</td></tr>'
                        + '<tr><td>应用</td><td>' + escapeHtml(appMap[d.app_id] || d.app_id) + ' <span style="color:#999;">(' + escapeHtml(d.app_id) + ')</span></td></tr>'
                        + '<tr><td>类型</td><td>' + escapeHtml(d.type || '-') + '</td></tr>'
                        + '<tr><td>模式</td><td>' + escapeHtml(d.mode || '-') + '</td></tr>'
                        + '<tr><td>状态</td><td>' + (statusMap[d.status] || escapeHtml(d.status)) + '</td></tr>'
                        + '<tr><td>参数</td><td>' + formatJsonField(d.params) + '</td></tr>'
                        + '<tr><td>结果</td><td>' + formatJsonField(d.result) + '</td></tr>'
                        + '<tr><td>错误信息</td><td>' + formatJsonField(d.error, true) + '</td></tr>'
                        + '<tr><td>创建时间</td><td>' + formatTime(d.create_time) + '</td></tr>'
                        + '<tr><td>开始时间</td><td>' + formatTime(d.start_time) + '</td></tr>'
                        + '<tr><td>完成时间</td><td>' + formatTime(d.finish_time) + '</td></tr>'
                        + '<tr><td>耗时</td><td>' + (d.duration_ms != null ? d.duration_ms + ' ms' : '-') + '</td></tr>'
                        + '</table></div>';
                    layer.open({
                        type: 1,
                        title: '任务详情 #' + d.id,
                        area: ['720px', '560px'],
                        maxmin: true,
                        content: html
                    });
                },
                error: function() {
                    layer.close(idx);
                    layer.msg('请求失败', {icon: 2, time: 2000});
                }
            });
        } else if (obj.event === 'cancel') {
            layer.confirm('确认取消该任务？', {icon: 3, title: '确认取消'}, function(index) {
                $.ajax({
                    url: API + '/tasks/' + encodeURIComponent(data.id) + '/cancel',
                    type: 'POST',
                    dataType: 'json',
                    success: function(res) {
                        if (res.code === 0) {
                            layer.msg('已取消', {icon: 1, time: 1500});
                            loadData(currentPage);
                        } else {
                            layer.msg(res.message || '取消失败', {icon: 2, time: 2000});
                        }
                    },
                    error: function() {
                        layer.msg('请求失败', {icon: 2, time: 2000});
                    }
                });
                layer.close(index);
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

    // 格式化 JSON 字段：尝试解析并语法高亮，非 JSON 则原样显示
    function formatJsonField(raw, isError) {
        if (!raw || raw === '-' || raw === 'null') return '-';
        var str = String(raw);
        var parsed = null;
        try { parsed = JSON.parse(str); } catch(e) {}
        if (parsed !== null && typeof parsed === 'object') {
            var formatted = JSON.stringify(parsed, null, 2);
            var highlighted = syntaxHighlight(formatted);
            // JSON 字符串值中的转义序列还原（先替换 \\ 避免干扰后续替换）
            highlighted = highlighted.replace(/\\\\/g, '\\').replace(/\\"/g, '"').replace(/\\n/g, '<br/>').replace(/\\r/g, '').replace(/\\t/g, '  ');
            return '<div class="json-viewer">' + highlighted + '</div>';
        }
        // 非 JSON 字符串，用 pre 原样显示，同时处理 \n 换行符
        var bg = isError ? '#fff2f0' : '#1e1e1e';
        var color = isError ? '#c00' : '#d4d4d4';
        var escaped = escapeHtml(str).replace(/\\\\/g, '\\').replace(/\\"/g, '"').replace(/\\n/g, '<br/>').replace(/\\r/g, '').replace(/\\t/g, '  ');
        return '<pre style="background:' + bg + ';color:' + color + ';padding:8px;border-radius:4px;max-height:250px;overflow:auto;font-size:12px;white-space:pre-wrap;word-break:break-all;">' + escaped + '</pre>';
    }

    // JSON 语法高亮
    function syntaxHighlight(json) {
        json = json.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
        return json.replace(/("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)/g, function (match) {
            var cls = 'json-number';
            if (/^"/.test(match)) {
                if (/:$/.test(match)) {
                    cls = 'json-key';
                } else {
                    cls = 'json-string';
                }
            } else if (/true|false/.test(match)) {
                cls = 'json-boolean';
            } else if (/null/.test(match)) {
                cls = 'json-null';
            }
            return '<span class="' + cls + '">' + match + '</span>';
        });
    }

    // 清空任务记录
    $('#btnClearTasks').click(function() {
        layer.confirm('确认清空全部任务记录？此操作不可恢复。', {icon: 3, title: '确认清空'}, function(index) {
            var idx = layer.load(1);
            $.ajax({
                url: API + '/tasks/clear',
                type: 'POST',
                dataType: 'json',
                success: function(res) {
                    layer.close(idx);
                    if (res.code === 0) {
                        layer.msg('任务记录已清空', {icon: 1, time: 1500});
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

    // 初始加载
    loadData(1);
});
</script>
</body>
</html>
