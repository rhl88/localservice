<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin;

use Illuminate\View\View;

class TaskController
{
    public function index(): View
    {
        return view('cmspro.localservice::Admin.tasks.index');
    }
}
