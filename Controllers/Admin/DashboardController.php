<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin;

use Illuminate\View\View;

class DashboardController
{
    public function index(): View
    {
        return view('cmspro.localservice::Admin.dashboard.index');
    }
}
