<?php

namespace App\Apps\CmsproLocalservice;

use App\Apps\CmsproLocalservice\Services\LocalServiceClient;
use App\Apps\CmsproLocalservice\Services\McpService;
use App\Apps\CmsproLocalservice\Services\ScriptService;
use App\Apps\CmsproLocalservice\Services\ServiceManager;
use Illuminate\Support\Facades\Route;
use Illuminate\Support\ServiceProvider as BaseServiceProvider;

class ServiceProvider extends BaseServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(
            base_path('app/Apps/CmsproLocalservice/Config/localservice.php'),
            'apps.cmspro.localservice'
        );

        // LocalServiceClient 供本应用及其他应用注入使用
        $this->app->singleton(LocalServiceClient::class, function () {
            return new LocalServiceClient();
        });

        $this->app->singleton(ServiceManager::class, function () {
            return new ServiceManager();
        });

        $this->app->singleton(ScriptService::class, function ($app) {
            return new ScriptService($app->make(LocalServiceClient::class));
        });

        $this->app->singleton(McpService::class, function () {
            return new McpService();
        });
    }

    public function boot(): void
    {
        $this->registerRoutes();
        $this->loadViews();
    }

    protected function registerRoutes(): void
    {
        // 后台页面路由
        Route::prefix('admin/cmspro/localservice')
            ->namespace('App\Apps\CmsproLocalservice\Controllers\Admin')
            ->middleware(['web', 'auth:admin'])
            ->group(base_path('app/Apps/CmsproLocalservice/Routes/admin.php'));

        // 后台API路由
        Route::prefix('api/admin/cmspro/localservice')
            ->namespace('App\Apps\CmsproLocalservice\Controllers\Admin\Api')
            ->middleware(['web', 'auth:admin'])
            ->group(base_path('app/Apps/CmsproLocalservice/Routes/admin_api.php'));
    }

    protected function loadViews(): void
    {
        $this->loadViewsFrom(
            base_path('app/Apps/CmsproLocalservice/Views'),
            'cmspro.localservice'
        );
    }
}
