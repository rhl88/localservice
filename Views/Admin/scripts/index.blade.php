<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>指令库 - 本地服务</title>
    <link rel="stylesheet" href="{{ asset('CmsProUi/component/pear/css/pear.css') }}">
    <link rel="stylesheet" href="{{ asset('CmsProUi/font-awesome/4.7.0/css/font-awesome.min.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/admin.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/variables.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/reset.css') }}">
    <style>
        .edit-form-label { display: inline-block; width: 80px; text-align: right; padding-right: 10px; color: #333; vertical-align: top; line-height: 36px; }
        .edit-form-item { margin-bottom: 12px; }
        .exec-result-box { max-height: 350px; overflow: auto; background: #1e1e1e; color: #d4d4d4; padding: 12px; border-radius: 4px; font-family: 'Cascadia Code', 'Consolas', monospace; font-size: 13px; line-height: 1.6; white-space: pre-wrap; word-break: break-all; }
        .exec-result-box .success { color: #4ec9b0; }
        .exec-result-box .error { color: #f44747; }
    </style>
</head>
<body>
<div class="pear-container">
    <div class="layui-card">
        <div class="layui-card-body">
            <form class="layui-form" lay-filter="scriptFilter">
                <div class="layui-form-item layui-inline-group">
                    <div class="layui-inline">
                        <label class="layui-form-label">关键字</label>
                        <div class="layui-input-inline" style="width:160px;">
                            <input type="text" name="keyword" class="layui-input" placeholder="名称或标识">
                        </div>
                    </div>
                    <div class="layui-inline">
                        <label class="layui-form-label">类型</label>
                        <div class="layui-input-inline" style="width:120px;">
                            <select name="type">
                                <option value="">全部类型</option>
                                <option value="bat">BAT</option>
                                <option value="ps1">PowerShell</option>
                                <option value="sh">Shell</option>
                                <option value="python">Python</option>
                            </select>
                        </div>
                    </div>
                    <div class="layui-inline">
                        <label class="layui-form-label">平台</label>
                        <div class="layui-input-inline" style="width:120px;">
                            <select name="platform">
                                <option value="">全部平台</option>
                                <option value="windows">Windows</option>
                                <option value="linux">Linux</option>
                                <option value="all">全平台</option>
                            </select>
                        </div>
                    </div>
                    <div class="layui-inline">
                        <button class="layui-btn layui-btn-sm" lay-submit lay-filter="btnSearch"><i class="fa fa-search"></i> 查询</button>
                        <button type="reset" class="layui-btn layui-btn-sm layui-btn-primary"><i class="fa fa-refresh"></i> 重置</button>
                    </div>
                </div>
            </form>
            <table id="scriptTable" lay-filter="scriptTable"></table>
        </div>
    </div>
</div>

<script type="text/html" id="scriptToolbar">
    <button class="layui-btn layui-btn-sm" lay-event="add"><i class="fa fa-plus"></i> 新建指令</button>
    <button class="layui-btn layui-btn-sm layui-btn-normal" lay-event="initLinux"><i class="fa fa-linux"></i> 初始化 Linux 脚本库</button>
</script>

<script type="text/html" id="scriptTypeTpl">
@verbatim
{{# var map = {bat:'BAT',ps1:'PowerShell',sh:'Shell',python:'Python'}; }}
<span class="layui-badge layui-bg-gray">{{ map[d.type] || d.type }}</span>
@endverbatim
</script>

<script type="text/html" id="scriptPlatformTpl">
@verbatim
{{# var map = {windows:'Windows',linux:'Linux',all:'全平台'}; var bg = {windows:'layui-bg-blue',linux:'layui-bg-orange',all:'layui-bg-green'}; }}
<span class="layui-badge {{ bg[d.platform] || 'layui-bg-gray' }}">{{ map[d.platform] || d.platform || 'all' }}</span>
@endverbatim
</script>

<script type="text/html" id="scriptStatusTpl">
@verbatim
{{# if(d.status === 1){ }}
<span class="layui-badge layui-bg-green">启用</span>
{{# } else { }}
<span class="layui-badge layui-bg-gray">禁用</span>
{{# } }}
@endverbatim
</script>

<script type="text/html" id="scriptBar">
@verbatim
<a class="layui-btn layui-btn-xs" lay-event="edit">编辑</a>
<a class="layui-btn layui-btn-xs layui-btn-normal" lay-event="exec">执行</a>
<a class="layui-btn layui-btn-xs layui-btn-danger" lay-event="del">删除</a>
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
        var params = {
            keyword: $('input[name="keyword"]').val(),
            type: $('select[name="type"]').val(),
            platform: $('select[name="platform"]').val(),
            page: 1,
            page_size: 20
        };
        $.ajax({
            url: API + '/scripts',
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
                    elem: '#scriptTable',
                    data: d.items || [],
                    cols: [[
                        {field: 'id', title: 'ID', width: 60},
                        {field: 'name', title: '标识', width: 120},
                        {field: 'title', title: '名称', minWidth: 140},
                        {field: 'type', title: '类型', width: 100, templet: '#scriptTypeTpl'},
                        {field: 'platform', title: '平台', width: 90, templet: '#scriptPlatformTpl'},
                        {field: 'description', title: '说明', minWidth: 200},
                        {field: 'status', title: '状态', width: 80, templet: '#scriptStatusTpl'},
                        {field: 'updated_at', title: '更新', width: 170},
                        {title: '操作', width: 180, toolbar: '#scriptBar', fixed: 'right'}
                    ]],
                    page: false,
                    limit: 20
                });
            },
            error: function() {
                layer.msg('请求失败', {icon: 2, time: 2000});
            }
        });
    }

    function escapeHtml(str) {
        if (str === null || str === undefined) return '';
        return String(str)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    // 查询
    form.on('submit(btnSearch)', function() { loadData(); return false; });

    // 工具栏事件
    table.on('toolbar(scriptTable)', function(obj) {
        if (obj.event === 'add') {
            openEditForm(null);
        } else if (obj.event === 'initLinux') {
            layer.confirm('确认初始化 Linux 常用脚本库？将自动插入 12 个系统运维脚本（仅首次有效，已初始化时会被拒绝）。', {
                icon: 3, title: '确认初始化', shade: 0.3
            }, function(index) {
                layer.close(index);
                var idx = layer.load(1, { content: '正在初始化...', shade: [0.3, '#000'] });
                $.ajax({
                    url: API + '/scripts/init-defaults',
                    type: 'POST',
                    dataType: 'json',
                    success: function(res) {
                        layer.close(idx);
                        if (res.code === 0) {
                            layer.msg(res.message || '初始化成功', {icon: 1, time: 2000});
                            loadData();
                        } else {
                            layer.msg(res.message || '初始化失败', {icon: 2, time: 3000});
                        }
                    },
                    error: function() {
                        layer.close(idx);
                        layer.msg('请求失败', {icon: 2, time: 2000});
                    }
                });
            });
        }
    });

    // 行工具栏事件
    table.on('tool(scriptTable)', function(obj) {
        var data = obj.data;
        if (obj.event === 'edit') {
            openEditForm(data);
        } else if (obj.event === 'exec') {
            openExecForm(data);
        } else if (obj.event === 'del') {
            layer.confirm('确认删除指令「' + escapeHtml(data.title) + '」？', {icon: 3, title: '确认删除'}, function(index) {
                $.ajax({
                    url: API + '/scripts/' + data.id,
                    type: 'DELETE',
                    dataType: 'json',
                    success: function(res) {
                        if (res.code === 0) {
                            layer.msg('删除成功', {icon: 1, time: 1500});
                            loadData();
                        } else {
                            layer.msg(res.message || '删除失败', {icon: 2, time: 2000});
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

    // 打开编辑/新增弹窗
    function openEditForm(data) {
        var isEdit = data && data.id;
        var title = isEdit ? '编辑指令 - ' + escapeHtml(data.title) : '新建指令';

        var html = '<div style="padding:15px;">'
            + '<form class="layui-form" lay-filter="scriptEditForm">'
            + '<input type="hidden" name="id" value="' + (data ? data.id : '') + '">'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">标识</label>'
            + '<div class="layui-input-inline" style="width:320px;">'
            + '<input type="text" name="name" class="layui-input" value="' + escapeHtml(data ? data.name : '') + '" placeholder="英文标识，如 my_backup" ' + (isEdit ? 'readonly style="background:#f5f5f5;"' : '') + '>'
            + '</div></div>'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">名称</label>'
            + '<div class="layui-input-inline" style="width:320px;">'
            + '<input type="text" name="title" class="layui-input" value="' + escapeHtml(data ? data.title : '') + '" placeholder="如：数据库备份">'
            + '</div></div>'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">类型</label>'
            + '<div class="layui-input-inline" style="width:160px;">'
            + '<select name="type">'
            + '<option value="bat"' + (data && data.type === 'bat' ? ' selected' : '') + '>BAT 批处理</option>'
            + '<option value="ps1"' + (data && data.type === 'ps1' ? ' selected' : '') + '>PowerShell</option>'
            + '<option value="sh"' + (data && data.type === 'sh' ? ' selected' : '') + '>Shell</option>'
            + '<option value="python"' + (data && data.type === 'python' ? ' selected' : '') + '>Python</option>'
            + '</select>'
            + '</div></div>'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">平台</label>'
            + '<div class="layui-input-inline" style="width:160px;">'
            + '<select name="platform">'
            + '<option value="all"' + (!data || data.platform === 'all' ? ' selected' : '') + '>全平台</option>'
            + '<option value="windows"' + (data && data.platform === 'windows' ? ' selected' : '') + '>Windows</option>'
            + '<option value="linux"' + (data && data.platform === 'linux' ? ' selected' : '') + '>Linux</option>'
            + '</select>'
            + '</div></div>'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">说明</label>'
            + '<div class="layui-input-inline" style="width:320px;">'
            + '<input type="text" name="description" class="layui-input" value="' + escapeHtml(data ? data.description : '') + '" placeholder="简要描述脚本功能">'
            + '</div></div>'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">脚本内容</label>'
            + '<div class="layui-input-inline" style="width:480px;">'
            + '<textarea name="content" class="layui-textarea" style="min-height:200px;font-family:Cascadia Code,Consolas,monospace;font-size:13px;" placeholder="在此编写脚本内容&#10;&#10;参数占位符：{param_name} 或 {param_name}">' + escapeHtml(data ? data.content : '') + '</textarea>'
            + '</div></div>'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">状态</label>'
            + '<div class="layui-input-inline" style="width:320px;">'
            + '<input type="checkbox" name="status" lay-skin="switch" lay-text="启用|禁用" ' + (!data || data.status === 1 ? 'checked' : '') + '>'
            + '</div></div>'
            + '</form></div>';

        layer.open({
            type: 1,
            title: title,
            area: ['620px', '520px'],
            shade: 0.3,
            content: html,
            btn: ['保存', '取消'],
            yes: function(index) {
                var formData = {
                    name: $('input[name="name"]').val(),
                    title: $('input[name="title"]').val(),
                    type: $('select[name="type"]').val(),
                    platform: $('select[name="platform"]').val(),
                    description: $('input[name="description"]').val(),
                    content: $('textarea[name="content"]').val(),
                    status: $('input[name="status"]').is(':checked') ? 1 : 0
                };

                if (!formData.name) { layer.msg('请输入标识', {icon: 2}); return; }
                if (!formData.title) { layer.msg('请输入名称', {icon: 2}); return; }
                if (!formData.content) { layer.msg('请输入脚本内容', {icon: 2}); return; }

                var idx = layer.load(1);
                var url = isEdit ? API + '/scripts/' + data.id : API + '/scripts';
                var method = isEdit ? 'PUT' : 'POST';

                $.ajax({
                    url: url,
                    type: method,
                    dataType: 'json',
                    contentType: 'application/json',
                    data: JSON.stringify(formData),
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
                form.render(null, 'scriptEditForm');
            }
        });
    }

    // 打开执行弹窗
    function openExecForm(data) {
        var html = '<div style="padding:15px;">'
            + '<div style="margin-bottom:12px;"><strong>' + escapeHtml(data.title) + '</strong> <span style="color:#999;">(' + escapeHtml(data.name) + ')</span></div>'
            + '<div class="edit-form-item">'
            + '<label class="edit-form-label">参数</label>'
            + '<div class="layui-input-inline" style="width:340px;">'
            + '<textarea name="exec_args" class="layui-textarea" style="min-height:80px;font-family:Cascadia Code,Consolas,monospace;font-size:13px;" placeholder="参数 JSON 格式，如：{&quot;host&quot;:&quot;127.0.0.1&quot;,&quot;port&quot;:&quot;3306&quot;}&#10;留空表示无参数"></textarea>'
            + '</div></div>'
            + '<div class="edit-form-item" style="margin-bottom:0;">'
            + '<label class="edit-form-label"></label>'
            + '<button type="button" class="layui-btn layui-btn-sm layui-btn-normal" id="btnDoExec"><i class="fa fa-play"></i> 立即执行</button>'
            + '</div>'
            + '<div id="execResultBox" style="margin-top:10px;display:none;"></div>'
            + '</div>';

        layer.open({
            type: 1,
            title: '执行指令 - ' + escapeHtml(data.title),
            area: ['620px', '480px'],
            shade: 0.3,
            content: html,
            btn: ['关闭'],
            success: function(layero) {
                layero.find('#btnDoExec').on('click', function() {
                    var argsStr = $('textarea[name="exec_args"]').val();
                    var args = {};
                    if (argsStr) {
                        try { args = JSON.parse(argsStr); }
                        catch(e) { layer.msg('参数格式错误，请输入合法的 JSON', {icon: 2}); return; }
                    }

                    var idx = layer.load(1, { content: '正在执行...', shade: [0.3, '#000'] });
                    $.ajax({
                        url: API + '/scripts/' + data.id + '/exec',
                        type: 'POST',
                        dataType: 'json',
                        contentType: 'application/json',
                        data: JSON.stringify({ args: args }),
                        success: function(res) {
                            layer.close(idx);
                            var box = $('#execResultBox');
                            if (res.code === 0) {
                                var stdout = (res.data && res.data.stdout) || '-';
                                var exitCode = (res.data && res.data.exit_code) || 0;
                                box.show().html(
                                    '<div class="exec-result-box">'
                                    + '<div class="success">Exit Code: ' + exitCode + '</div>'
                                    + '<hr style="border-color:#333;">'
                                    + escapeHtml(stdout)
                                    + '</div>'
                                );
                            } else {
                                box.show().html(
                                    '<div class="exec-result-box">'
                                    + '<div class="error">' + escapeHtml(res.message || '执行失败') + '</div>'
                                    + '</div>'
                                );
                            }
                        },
                        error: function() {
                            layer.close(idx);
                            layer.msg('请求失败', {icon: 2, time: 2000});
                        }
                    });
                });
            }
        });
    }

    loadData();
});
</script>
</body>
</html>
