<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>MCP 服务 - 本地服务</title>
    <link rel="stylesheet" href="{{ asset('CmsProUi/component/pear/css/pear.css') }}">
    <link rel="stylesheet" href="{{ asset('CmsProUi/font-awesome/4.7.0/css/font-awesome.min.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/admin.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/variables.css') }}">
    <link rel="stylesheet" href="{{ asset('Admin/css/reset.css') }}">
    <style>
        .mcp-status-bar { display: flex; align-items: center; gap: 8px; padding: 10px 15px; border-radius: 4px; font-size: 13px; margin-bottom: 15px; }
        .mcp-status-bar.on { background: #f0f9eb; color: #67c23a; }
        .mcp-status-bar.off { background: #f8f9fb; color: #999; }
        .mcp-section { margin-bottom: 20px; }
        .mcp-section .section-title { font-size: 15px; font-weight: 600; padding: 8px 0; border-bottom: 2px solid var(--global-primary-color); margin-bottom: 15px; color: #333; }
        .mcp-section .section-title i { margin-right: 6px; color: var(--global-primary-color); }
        .mcp-item { margin-bottom: 15px; }
        .mcp-item .item-label { display: inline-block; width: 130px; text-align: right; padding-right: 10px; color: #333; font-weight: 500; vertical-align: top; }
        .mcp-item .item-tips { color: #999; font-size: 12px; margin-top: 4px; padding-left: 140px; line-height: 1.7; }
        .mcp-code { background: #f5f6f8; border: 1px solid #e8eaee; border-radius: 4px; padding: 8px 12px; font-family: Consolas, Monaco, 'Courier New', monospace; font-size: 12px; color: #333; word-break: break-all; display: inline-block; max-width: 620px; vertical-align: top; }
        .mcp-prompt { background: #f8f9fb; border: 1px solid #ebeef5; border-radius: 4px; padding: 12px 15px; font-family: Consolas, Monaco, 'Courier New', monospace; font-size: 12px; color: #333; white-space: pre-wrap; line-height: 1.8; max-height: 260px; overflow-y: auto; }
        .risk-tag { display: inline-block; padding: 1px 7px; border-radius: 3px; font-size: 12px; }
        .risk-low { background: #f0f9eb; color: #67c23a; }
        .risk-medium { background: #fdf6ec; color: #e6a23c; }
        .risk-high { background: #fef0f0; color: #f56c6c; }
        .tag-on { display: inline-block; padding: 1px 7px; border-radius: 3px; font-size: 12px; background: #f0f9eb; color: #67c23a; }
        .tag-off { display: inline-block; padding: 1px 7px; border-radius: 3px; font-size: 12px; background: #f8f9fb; color: #999; }
        .tool-group { border: 1px solid #ebeef5; border-radius: 4px; margin-bottom: 10px; }
        .tool-group .group-head { padding: 8px 12px; background: #fafafa; border-bottom: 1px solid #ebeef5; font-weight: 600; font-size: 13px; cursor: pointer; display: flex; justify-content: space-between; align-items: center; }
        .tool-group .group-body { padding: 10px 12px; display: none; }
        .tool-group.open .group-body { display: block; }
        .tool-row { display: flex; align-items: center; padding: 5px 0; font-size: 13px; }
        .tool-row .tool-check { flex: 0 0 30px; }
        .tool-row .tool-name { flex: 0 0 170px; font-weight: 500; color: #333; }
        .tool-row .tool-risk { flex: 0 0 80px; }
        .tool-row .tool-desc { flex: 1; color: #888; font-size: 12px; }
        .key-modal-toolbar { display: flex; align-items: center; gap: 8px; padding-bottom: 10px; border-bottom: 1px solid #ebeef5; margin-bottom: 10px; }
    </style>
</head>
<body>
<div class="pear-container" style="padding:15px;">
    <div class="layui-card">
        <div class="layui-card-body" style="padding:20px;">
            <div class="layui-tab layui-tab-brief" lay-filter="mcpTab" style="margin-bottom:0;">
                <ul class="layui-tab-title">
                    <li class="layui-this">接入与体验</li>
                    <li>安全授权</li>
                </ul>
                <div class="layui-tab-content" style="padding:18px 0 0;">

                    {{-- ========== Tab 1：接入与体验 ========== --}}
                    <div class="layui-tab-item layui-show">
                        <div class="mcp-status-bar off" id="mcpStatus">
                            <i class="fa fa-circle-o-notch"></i><span id="mcpStatusText">检测中...</span>
                        </div>

                        <div class="mcp-section">
                            <div class="section-title"><i class="fa fa-link"></i>服务地址</div>

                            <div class="mcp-item">
                                <label class="item-label">MCP 端点</label>
                                <span class="mcp-code" id="mcpEndpoint">加载中...</span>
                                <button type="button" class="layui-btn layui-btn-xs layui-btn-primary" data-copy="mcpEndpoint"><i class="fa fa-copy"></i> 复制</button>
                                <div class="item-tips">
                                    外部 AI 工具（Claude Code、Trae、Cursor、Codex 等）的 MCP 服务地址。
                                    地址含随机前缀以降低被扫描风险，可点「重新生成」更换。
                                </div>
                            </div>

                            <div class="mcp-item">
                                <label class="item-label">路由前缀</label>
                                <span class="mcp-code" id="mcpPrefix">-</span>
                                <button type="button" class="layui-btn layui-btn-xs layui-btn-warm" id="btnRegenPrefix"><i class="fa fa-refresh"></i> 重新生成</button>
                                <div class="item-tips">更换后所有已配置的客户端需同步更新接入地址。</div>
                            </div>
                        </div>

                        <div class="mcp-section">
                            <div class="section-title"><i class="fa fa-magic"></i>接入提示词</div>
                            <div class="mcp-item">
                                <div class="layui-input-block" style="margin-left:0;">
                                    <div class="mcp-prompt" id="mcpPrompt">加载中...</div>
                                    <div style="margin-top:10px;">
                                        <button type="button" class="layui-btn layui-btn-sm layui-btn-normal" data-copy="mcpPrompt"><i class="fa fa-copy"></i> 复制提示词</button>
                                        <span class="item-tips" style="display:inline-block;padding-left:10px;margin-top:0;">
                                            把提示词发给 AI 工具，即可自动完成接入配置
                                        </span>
                                    </div>
                                </div>
                            </div>
                        </div>

                        <div class="mcp-section">
                            <div class="section-title"><i class="fa fa-info-circle"></i>能力概览</div>
                            <div class="mcp-item">
                                <div style="font-size:13px;line-height:2.1;color:#555;">
                                    本地服务把本机能力通过 MCP 协议开放给外部 AI 工具，当前共
                                    <b id="toolTotal">-</b> 个工具：
                                    <span class="risk-tag risk-low">低风险 <b id="riskLow">-</b></span>
                                    <span class="risk-tag risk-medium">中风险 <b id="riskMedium">-</b></span>
                                    <span class="risk-tag risk-high">高风险 <b id="riskHigh">-</b></span>
                                    <br>· <b>低风险</b>：查询类操作（系统信息、文件读取、进程/服务列表等），只读无副作用
                                    <br>· <b>中风险</b>：可恢复的修改（服务启停、浏览器点击输入、Cookie 设置等）
                                    <br>· <b>高风险</b>：破坏性操作（写删文件、启停进程、执行命令与脚本等），<b>调用时需人工确认</b>
                                </div>
                            </div>
                        </div>
                    </div>

                    {{-- ========== Tab 2：安全授权 ========== --}}
                    <div class="layui-tab-item">
                        <div class="mcp-section">
                            <div class="section-title"><i class="fa fa-shield"></i>外部访问</div>

                            <div class="mcp-item">
                                <label class="item-label">启用服务</label>
                                <input type="checkbox" id="swEnabled" lay-skin="switch" lay-text="启用|停用">
                                <div class="item-tips">停用后所有 MCP 客户端将无法连接。</div>
                            </div>

                            <div class="mcp-item">
                                <label class="item-label">IP 白名单</label>
                                <span class="mcp-code" id="ipAllowlistText">加载中...</span>
                                <button type="button" class="layui-btn layui-btn-xs layui-btn-normal" id="btnEditIp"><i class="fa fa-pencil"></i> 修改</button>
                                <div class="item-tips">
                                    支持 <code>*</code>（放行全部）、单个 IP、CIDR（如 10.0.0.0/8）、IP 区间（如 192.168.1.1-192.168.1.50）。
                                    仅放行可信来源，建议填写 AI 工具所在机器的公网出口 IP。
                                </div>
                            </div>

                            <div class="mcp-item">
                                <label class="item-label">调用限流</label>
                                <span style="font-size:13px;">每 <b id="rateWindow">-</b> 秒最多 <b id="rateMax">-</b> 次</span>
                                <button type="button" class="layui-btn layui-btn-xs layui-btn-normal" id="btnEditRate"><i class="fa fa-pencil"></i> 修改</button>
                                <div class="item-tips">按「凭据 + 来源 IP」计数，超出后返回 429。</div>
                            </div>
                        </div>

                        <div class="mcp-section">
                            <div class="section-title" style="display:flex;justify-content:space-between;align-items:center;">
                                <span><i class="fa fa-key"></i>访问凭据</span>
                                <button type="button" class="layui-btn layui-btn-sm layui-btn-normal" id="btnAddKey"><i class="fa fa-plus"></i> 创建凭据</button>
                            </div>
                            <table class="layui-table" lay-skin="line">
                                <thead>
                                    <tr>
                                        <th style="width:150px;">凭据名称</th>
                                        <th style="width:150px;">KEY</th>
                                        <th style="width:120px;">有效期</th>
                                        <th style="width:130px;">已授权</th>
                                        <th style="width:90px;">状态</th>
                                        <th>操作</th>
                                    </tr>
                                </thead>
                                <tbody id="keyTableBody">
                                    <tr><td colspan="6" style="text-align:center;color:#999;padding:24px;">加载中...</td></tr>
                                </tbody>
                            </table>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>
</div>

<script src="{{ asset('CmsProUi/component/layui/layui.js') }}"></script>
<script src="{{ asset('CmsProUi/component/pear/pear.js') }}"></script>
<script>
layui.use(['layer', 'jquery', 'form'], function () {
    var layer = layui.layer,
        $ = layui.jquery,
        form = layui.form;

    var API = '/api/admin/cmspro/localservice/mcp';

    $.ajaxSetup({
        headers: { 'X-CSRF-TOKEN': '{{ csrf_token() }}' },
        statusCode: {
            401: function () {
                layer.msg('登录已过期，请重新登录', { icon: 2, time: 1500 }, function () {
                    window.location.href = '/admin/login?redirect=' + encodeURIComponent(window.location.pathname);
                });
            }
        }
    });

    function escapeHtml(s) {
        return $('<div>').text(s === undefined || s === null ? '' : String(s)).html();
    }

    function riskLabel(risk) {
        var map = { low: '低风险', medium: '中风险', high: '高风险' };
        return '<span class="risk-tag risk-' + risk + '">' + (map[risk] || risk) + '</span>';
    }

    // ============ 工具与配置 ============

    var toolsCache = [];

    function loadTools() {
        $.get(API + '/tools', function (res) {
            if (res.code !== 0) { return; }
            toolsCache = res.data.tools || [];
            $('#toolTotal').text(res.data.total);
            $('#riskLow').text((res.data.risk_counts || {}).low || 0);
            $('#riskMedium').text((res.data.risk_counts || {}).medium || 0);
            $('#riskHigh').text((res.data.risk_counts || {}).high || 0);
        });
    }

    function loadConfig() {
        $.get(API + '/config', function (res) {
            if (res.code !== 0) { return; }
            var d = res.data;

            $('#mcpPrompt').text(d.prompt || '');
            $('#mcpPrefix').text(d.path_prefix || '-');
            $('#ipAllowlistText').text((d.ip_allowlist || []).join('、') || '-');
            $('#rateMax').text(d.rate_limit_max);
            $('#rateWindow').text(d.rate_limit_window);
            $('#swEnabled').prop('checked', !!d.enabled);
            form.render('checkbox');

            // 端点地址：协议 + 主机 + 端口 + MCP 路径
            var proto = location.protocol;
            var host = location.hostname;
            var endpoint = proto + '//' + host + ':' + '{{ (string) config("apps.cmspro.localservice.service_settings.port", "9501") }}' + (d.mcp_path || '');
            $('#mcpEndpoint').text(endpoint);

            $('#mcpStatus').removeClass('on off').addClass(d.enabled ? 'on' : 'off');
            $('#mcpStatusText').text(d.enabled ? 'MCP 服务已启用 · 端点 ' + (d.mcp_path || '') : 'MCP 服务未启用（在「安全授权」中开启）');
        });
    }

    // 复制
    $(document).on('click', '[data-copy]', function () {
        var id = $(this).data('copy');
        var text = $('#' + id).text();
        if (!text || text === '加载中...') {
            layer.msg('暂无可复制内容', { icon: 2 });
            return;
        }
        var ta = $('<textarea>').val(text).appendTo('body').select();
        try { document.execCommand('copy'); layer.msg('已复制', { icon: 1 }); }
        catch (e) { layer.msg('复制失败，请手动选择', { icon: 2 }); }
        ta.remove();
    });

    // 重新生成路由前缀
    $('#btnRegenPrefix').on('click', function () {
        layer.confirm('重新生成后，已配置的客户端需同步更新接入地址，确认继续？', { icon: 3 }, function (idx) {
            layer.close(idx);
            $.post(API + '/config/regenerate-prefix', {}, function (res) {
                if (res.code === 0) {
                    layer.msg('已更新', { icon: 1 }, function () { loadConfig(); });
                } else {
                    layer.msg(res.message || '操作失败', { icon: 2 });
                }
            });
        });
    });

    // 启用开关
    form.on('switch()', function (obj) {
        if (obj.elem.id !== 'swEnabled') { return; }
        saveConfig({ enabled: obj.elem.checked }, obj);
    });

    function saveConfig(payload, swObj) {
        $.ajax({
            url: API + '/config',
            type: 'POST',
            data: JSON.stringify(payload),
            contentType: 'application/json',
            success: function (res) {
                if (res.code === 0) {
                    layer.msg(res.message || '已保存', { icon: 1 }, function () { loadConfig(); });
                } else {
                    layer.msg(res.message || '保存失败', { icon: 2 });
                    if (swObj) { swObj.elem.checked = !swObj.elem.checked; form.render('checkbox'); }
                }
            },
            error: function () {
                layer.msg('保存失败', { icon: 2 });
                if (swObj) { swObj.elem.checked = !swObj.elem.checked; form.render('checkbox'); }
            }
        });
    }

    // 修改 IP 白名单
    $('#btnEditIp').on('click', function () {
        $.get(API + '/config', function (res) {
            if (res.code !== 0) { return; }
            var current = (res.data.ip_allowlist || []).join('\n');
            layer.prompt({
                formType: 2,
                title: 'IP 白名单（每行一条，* 表示放行全部）',
                value: current,
                area: ['480px', '220px']
            }, function (value, idx) {
                var list = value.split('\n').map(function (v) { return $.trim(v); }).filter(function (v) { return v !== ''; });
                if (list.length === 0) {
                    layer.msg('至少保留一条，或填写 * 放行全部', { icon: 2 });
                    return;
                }
                layer.close(idx);
                saveConfig({ ip_allowlist: list });
            });
        });
    });

    // 修改限流
    $('#btnEditRate').on('click', function () {
        $.get(API + '/config', function (res) {
            if (res.code !== 0) { return; }
            layer.open({
                type: 1,
                title: '调用限流',
                area: ['420px', 'auto'],
                content: '<div style="padding:20px 25px;">' +
                    '<div style="margin-bottom:14px;"><label style="display:inline-block;width:100px;text-align:right;padding-right:10px;">窗口（秒）</label>' +
                    '<input type="number" id="f_rate_window" class="layui-input" style="display:inline-block;width:160px;" value="' + res.data.rate_limit_window + '"></div>' +
                    '<div><label style="display:inline-block;width:100px;text-align:right;padding-right:10px;">最大次数</label>' +
                    '<input type="number" id="f_rate_max" class="layui-input" style="display:inline-block;width:160px;" value="' + res.data.rate_limit_max + '"></div>' +
                    '</div>',
                btn: ['保存', '取消'],
                yes: function (idx) {
                    var win = parseInt($('#f_rate_window').val(), 10) || 60;
                    var max = parseInt($('#f_rate_max').val(), 10) || 300;
                    layer.close(idx);
                    saveConfig({ rate_limit_max: max, rate_limit_window: win });
                }
            });
        });
    });

    // ============ 凭据管理 ============

    function loadKeys() {
        $.get(API + '/keys', function (res) {
            if (res.code !== 0) { return; }
            var list = res.data || [];
            if (list.length === 0) {
                $('#keyTableBody').html('<tr><td colspan="6" style="text-align:center;color:#999;padding:24px;">暂无凭据，点右上角「创建凭据」接入 AI 工具</td></tr>');
                return;
            }
            var html = '';
            $.each(list, function (i, k) {
                var status = k.effective
                    ? '<span class="tag-on">运行中</span>'
                    : (k.expired ? '<span class="tag-off">已过期</span>' : '<span class="tag-off">已停用</span>');
                html += '<tr>' +
                    '<td>' + escapeHtml(k.name) + '</td>' +
                    '<td style="font-family:monospace;">' + escapeHtml(k.token_masked) + '</td>' +
                    '<td>' + escapeHtml(k.expires_label) + '</td>' +
                    '<td><b>' + k.granted_count + '</b> / ' + k.total_count + '</td>' +
                    '<td>' + status + '</td>' +
                    '<td>' +
                        '<button class="layui-btn layui-btn-xs layui-btn-normal" onclick="mcpEditPerm(' + k.id + ')">权限</button>' +
                        '<button class="layui-btn layui-btn-xs" onclick="mcpEditKey(' + k.id + ')">编辑</button>' +
                        '<button class="layui-btn layui-btn-xs layui-btn-primary" onclick="mcpToggleKey(' + k.id + ')">' + (k.active ? '停用' : '启用') + '</button>' +
                        '<button class="layui-btn layui-btn-xs layui-btn-danger" onclick="mcpDeleteKey(' + k.id + ')">删除</button>' +
                    '</td>' +
                    '</tr>';
            });
            $('#keyTableBody').html(html);
        });
    }

    $('#btnAddKey').on('click', function () {
        layer.open({
            type: 1,
            title: '创建访问凭据',
            area: ['520px', 'auto'],
            content: '<div style="padding:20px 25px;">' +
                '<div style="margin-bottom:14px;"><label style="display:inline-block;width:90px;text-align:right;padding-right:10px;">名称</label>' +
                '<input type="text" id="f_key_name" class="layui-input" style="display:inline-block;width:300px;" placeholder="如 我的 Trae"></div>' +
                '<div><label style="display:inline-block;width:90px;text-align:right;padding-right:10px;">有效期</label>' +
                '<select id="f_key_expires" class="layui-input" style="display:inline-block;width:300px;">' +
                '<option value="-1">永不过期</option><option value="7">7 天</option>' +
                '<option value="30">30 天</option><option value="90">90 天</option></select></div>' +
                '<div style="margin-top:12px;padding-left:100px;color:#999;font-size:12px;">新凭据默认仅授权低风险工具，创建后可点「权限」按需开启更多能力。</div>' +
                '</div>',
            btn: ['创建', '取消'],
            yes: function (idx) {
                var name = $.trim($('#f_key_name').val());
                if (!name) { layer.msg('请填写凭据名称', { icon: 2 }); return; }
                $.ajax({
                    url: API + '/keys',
                    type: 'POST',
                    data: JSON.stringify({ name: name, expires_days: parseInt($('#f_key_expires').val(), 10) }),
                    contentType: 'application/json',
                    success: function (res) {
                        if (res.code !== 0) { layer.msg(res.message || '创建失败', { icon: 2 }); return; }
                        layer.close(idx);
                        layer.open({
                            type: 1,
                            title: '凭据已创建',
                            area: ['600px', 'auto'],
                            content: '<div style="padding:20px 25px;">' +
                                '<div style="color:#f56c6c;margin-bottom:12px;"><i class="fa fa-exclamation-triangle"></i> 令牌仅在此显示一次，请立即复制保存。</div>' +
                                '<div style="background:#f8f9fb;border:1px solid #ebeef5;border-radius:4px;padding:12px;font-family:monospace;word-break:break-all;" id="newToken">' + escapeHtml(res.data.token) + '</div>' +
                                '</div>',
                            btn: ['复制令牌', '关闭'],
                            yes: function (i2) {
                                var ta = $('<textarea>').val(res.data.token).appendTo('body').select();
                                try { document.execCommand('copy'); layer.msg('已复制', { icon: 1 }); }
                                catch (e) { layer.msg('复制失败', { icon: 2 }); }
                                ta.remove();
                                layer.close(i2);
                                loadKeys();
                            },
                            btn2: function () { loadKeys(); }
                        });
                    }
                });
            }
        });
    });

    window.mcpEditKey = function (id) {
        $.get(API + '/keys/' + id, function (res) {
            if (res.code !== 0) { return; }
            var k = res.data;
            layer.open({
                type: 1,
                title: '编辑凭据',
                area: ['520px', 'auto'],
                content: '<div style="padding:20px 25px;">' +
                    '<div style="margin-bottom:14px;"><label style="display:inline-block;width:90px;text-align:right;padding-right:10px;">名称</label>' +
                    '<input type="text" id="e_key_name" class="layui-input" style="display:inline-block;width:300px;" value="' + escapeHtml(k.name) + '"></div>' +
                    '<div><label style="display:inline-block;width:90px;text-align:right;padding-right:10px;">有效期</label>' +
                    '<select id="e_key_expires" class="layui-input" style="display:inline-block;width:300px;">' +
                    '<option value="-1"' + (k.expires_days === -1 ? ' selected' : '') + '>永不过期</option>' +
                    '<option value="7"' + (k.expires_days === 7 ? ' selected' : '') + '>7 天</option>' +
                    '<option value="30"' + (k.expires_days === 30 ? ' selected' : '') + '>30 天</option>' +
                    '<option value="90"' + (k.expires_days === 90 ? ' selected' : '') + '>90 天</option></select></div>' +
                    '</div>',
                btn: ['保存', '取消'],
                yes: function (idx) {
                    var name = $.trim($('#e_key_name').val());
                    if (!name) { layer.msg('请填写凭据名称', { icon: 2 }); return; }
                    $.ajax({
                        url: API + '/keys/' + id,
                        type: 'PUT',
                        data: JSON.stringify({ name: name, expires_days: parseInt($('#e_key_expires').val(), 10) }),
                        contentType: 'application/json',
                        success: function (r) {
                            if (r.code === 0) { layer.close(idx); layer.msg('已保存', { icon: 1 }, loadKeys); }
                            else { layer.msg(r.message || '保存失败', { icon: 2 }); }
                        }
                    });
                }
            });
        });
    };

    window.mcpToggleKey = function (id) {
        $.post(API + '/keys/' + id + '/toggle', {}, function (res) {
            if (res.code === 0) { layer.msg(res.message, { icon: 1 }, loadKeys); }
            else { layer.msg(res.message || '操作失败', { icon: 2 }); }
        });
    };

    window.mcpDeleteKey = function (id) {
        layer.confirm('删除后该凭据将立即失效，确认删除？', { icon: 3 }, function (idx) {
            layer.close(idx);
            $.ajax({
                url: API + '/keys/' + id,
                type: 'DELETE',
                success: function (res) {
                    if (res.code === 0) { layer.msg('已删除', { icon: 1 }, loadKeys); }
                    else { layer.msg(res.message || '删除失败', { icon: 2 }); }
                }
            });
        });
    };

    // 权限管理弹窗（按分类折叠 + 勾选工具）
    window.mcpEditPerm = function (id) {
        $.get(API + '/keys/' + id, function (res) {
            if (res.code !== 0) { return; }
            var k = res.data;
            var granted = {};
            $.each(k.tool_ids || [], function (i, t) { granted[t] = true; });

            // 按分类分组
            var groups = {};
            $.each(toolsCache, function (i, t) {
                var c = t.category || '其他';
                if (!groups[c]) { groups[c] = []; }
                groups[c].push(t);
            });

            var html = '<div style="padding:15px 20px;">' +
                '<div class="key-modal-toolbar">' +
                '<span style="font-size:13px;color:#666;">凭据：<b>' + escapeHtml(k.name) + '</b></span>' +
                '<span style="margin-left:auto;font-size:13px;">已授权 <b id="permCount">' + k.granted_count + '</b> / ' + k.total_count + '</span>' +
                '<button type="button" class="layui-btn layui-btn-xs layui-btn-primary" id="btnLowRiskOnly">仅授权低风险</button>' +
                '<button type="button" class="layui-btn layui-btn-xs layui-btn-primary" id="btnSelectAll">全选</button>' +
                '<button type="button" class="layui-btn layui-btn-xs layui-btn-primary" id="btnClearAll">清空</button>' +
                '</div>' +
                '<div style="max-height:420px;overflow-y:auto;">';

            var catIndex = 0;
            $.each(groups, function (cat, list) {
                var catGranted = 0;
                $.each(list, function (i, t) { if (granted[t.tool_id]) { catGranted++; } });
                html += '<div class="tool-group' + (catIndex === 0 ? ' open' : '') + '" data-cat="' + escapeHtml(cat) + '">' +
                    '<div class="group-head"><span>' + escapeHtml(cat) + '</span>' +
                    '<span style="font-weight:400;font-size:12px;color:#888;" class="cat-count">' + catGranted + '/' + list.length + ' 已授权</span></div>' +
                    '<div class="group-body">';
                $.each(list, function (i, t) {
                    html += '<div class="tool-row">' +
                        '<div class="tool-check"><input type="checkbox" class="perm-tool" value="' + escapeHtml(t.tool_id) + '"' + (granted[t.tool_id] ? ' checked' : '') + '></div>' +
                        '<div class="tool-name">' + escapeHtml(t.name) + '</div>' +
                        '<div class="tool-risk">' + riskLabel(t.risk_level) + '</div>' +
                        '<div class="tool-desc">' + escapeHtml(t.description || '') + '</div>' +
                        '</div>';
                });
                html += '</div></div>';
                catIndex++;
            });
            html += '</div></div>';

            layer.open({
                type: 1,
                title: '凭据权限管理',
                area: ['860px', '580px'],
                content: html,
                btn: ['保存授权', '取消'],
                success: function () {
                    // 分类折叠
                    $('.tool-group .group-head').on('click', function () {
                        $(this).parent().toggleClass('open');
                    });
                    // 勾选联动计数
                    $('.perm-tool').on('change', function () { updatePermCount(); });
                    // 仅授权低风险
                    $('#btnLowRiskOnly').on('click', function () {
                        $('.perm-tool').each(function () {
                            var rid = $(this).val();
                            var t = null;
                            $.each(toolsCache, function (i, x) { if (x.tool_id === rid) { t = x; return false; } });
                            $(this).prop('checked', t && t.risk_level === 'low');
                        });
                        updatePermCount();
                    });
                    $('#btnSelectAll').on('click', function () {
                        $('.perm-tool').prop('checked', true);
                        updatePermCount();
                    });
                    $('#btnClearAll').on('click', function () {
                        $('.perm-tool').prop('checked', false);
                        updatePermCount();
                    });
                },
                yes: function (idx) {
                    var selected = [];
                    $('.perm-tool:checked').each(function () { selected.push($(this).val()); });
                    $.ajax({
                        url: API + '/keys/' + id,
                        type: 'PUT',
                        data: JSON.stringify({ tool_ids: selected }),
                        contentType: 'application/json',
                        success: function (r) {
                            if (r.code === 0) {
                                layer.close(idx);
                                layer.msg('授权已保存', { icon: 1 }, loadKeys);
                            } else {
                                layer.msg(r.message || '保存失败', { icon: 2 });
                            }
                        }
                    });
                }
            });
        });
    };

    function updatePermCount() {
        var total = $('.perm-tool').length;
        var checked = $('.perm-tool:checked').length;
        $('#permCount').text(checked);

        // 各分类计数
        $('.tool-group').each(function () {
            var $g = $(this);
            var all = $g.find('.perm-tool').length;
            var on = $g.find('.perm-tool:checked').length;
            $g.find('.cat-count').text(on + '/' + all + ' 已授权');
        });
    }

    // ============ 初始化 ============
    loadTools();
    loadConfig();
    loadKeys();
});
</script>
</body>
</html>