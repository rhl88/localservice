<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin;

use Illuminate\View\View;

class SettingController
{
    public function index(): View
    {
        return view('cmspro.localservice::Admin.settings.index');
    }
}
