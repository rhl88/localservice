<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin;

use Illuminate\View\View;

class ScriptController
{
    public function index(): View
    {
        return view('cmspro.localservice::Admin.scripts.index');
    }
}
