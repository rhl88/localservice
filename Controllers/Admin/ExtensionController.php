<?php

namespace App\Apps\CmsproLocalservice\Controllers\Admin;

use Illuminate\View\View;

class ExtensionController
{
    public function index(): View
    {
        return view('cmspro.localservice::Admin.extensions.index');
    }
}
