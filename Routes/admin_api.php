<?php

use App\Apps\CmsproLocalservice\Controllers\Admin\Api\ExtensionApiController;
use App\Apps\CmsproLocalservice\Controllers\Admin\Api\LogApiController;
use App\Apps\CmsproLocalservice\Controllers\Admin\Api\ScriptApiController;
use App\Apps\CmsproLocalservice\Controllers\Admin\Api\ServiceApiController;
use App\Apps\CmsproLocalservice\Controllers\Admin\Api\SettingApiController;
use App\Apps\CmsproLocalservice\Controllers\Admin\Api\TaskApiController;
use Illuminate\Support\Facades\Route;

// 服务控制
Route::get('/service/status', [ServiceApiController::class, 'status']);
Route::get('/service/installed-apps', [ServiceApiController::class, 'installedApps']);
Route::post('/service/install-script', [ServiceApiController::class, 'installScript']);
Route::post('/service/uninstall-script', [ServiceApiController::class, 'uninstallScript']);
Route::post('/service/start', [ServiceApiController::class, 'start']);
Route::post('/service/stop', [ServiceApiController::class, 'stop']);
Route::post('/service/restart', [ServiceApiController::class, 'restart']);

// 设置
Route::get('/settings', [SettingApiController::class, 'index']);
Route::post('/settings', [SettingApiController::class, 'save']);
Route::post('/settings/regenerate-token', [SettingApiController::class, 'regenerateToken']);
Route::post('/settings/regenerate-open-api-key', [SettingApiController::class, 'regenerateOpenApiKey']);

// 任务
Route::get('/tasks', [TaskApiController::class, 'index']);
Route::get('/tasks/{id}', [TaskApiController::class, 'show']);
Route::post('/tasks/{id}/cancel', [TaskApiController::class, 'cancel']);
Route::post('/tasks/clear', [TaskApiController::class, 'clear']);

// 日志
Route::get('/logs', [LogApiController::class, 'index']);
Route::post('/logs/clear', [LogApiController::class, 'clear']);

// 拓展
Route::get('/extensions', [ExtensionApiController::class, 'index']);
Route::get('/extensions/{name}', [ExtensionApiController::class, 'show']);
Route::put('/extensions/{name}', [ExtensionApiController::class, 'update']);
Route::post('/extensions/{name}/test', [ExtensionApiController::class, 'test']);
Route::post('/extensions/{name}/install', [ExtensionApiController::class, 'install']);
Route::post('/extensions/{name}/install-by-command', [ExtensionApiController::class, 'installByCommand']);
Route::get('/extensions/{name}/default-install-command', [ExtensionApiController::class, 'getDefaultInstallCommand']);
Route::get('/extensions/{name}/install-task/{taskId}', [ExtensionApiController::class, 'getInstallTaskResult']);

// 指令库
Route::prefix('scripts')->controller(ScriptApiController::class)->group(function () {
    Route::get('/', 'index');
    Route::get('all', 'all');
    Route::get('{id}', 'show');
    Route::post('/', 'store');
    Route::put('{id}', 'update');
    Route::delete('{id}', 'destroy');
    Route::post('{id}/exec', 'execute');
    Route::post('init-defaults', 'initDefaults');
});
