<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin;

use Illuminate\View\View;

/**
 * MCP 服务管理页。
 *
 * 对齐宝塔 MCP 的界面结构：接入与体验（地址 + 提示词）/ 安全授权（访问控制 + 凭据）/
 * 任务记录（MCP 调用审计，复用日志页数据源）。
 */
class McpController
{
    public function index(): View
    {
        return view('cmspro.localservice::Admin.mcp.index');
    }
}