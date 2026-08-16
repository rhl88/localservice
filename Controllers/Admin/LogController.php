<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin;

use Illuminate\View\View;

class LogController
{
    public function index(): View
    {
        return view('cmspro.localservice::Admin.logs.index');
    }
}
