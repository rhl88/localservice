<?php

use App\Apps\CmsproLocalservice\Controllers\Admin\DashboardController;
use App\Apps\CmsproLocalservice\Controllers\Admin\ExtensionController;
use App\Apps\CmsproLocalservice\Controllers\Admin\LogController;
use App\Apps\CmsproLocalservice\Controllers\Admin\McpController;
use App\Apps\CmsproLocalservice\Controllers\Admin\ScriptController;
use App\Apps\CmsproLocalservice\Controllers\Admin\SettingController;
use App\Apps\CmsproLocalservice\Controllers\Admin\TaskController;
use Illuminate\Support\Facades\Route;

Route::get('/', [DashboardController::class, 'index']);
Route::get('/dashboard', [DashboardController::class, 'index']);
Route::get('/tasks', [TaskController::class, 'index']);
Route::get('/logs', [LogController::class, 'index']);
Route::get('/extensions', [ExtensionController::class, 'index']);
Route::get('/scripts', [ScriptController::class, 'index']);
Route::get('/settings', [SettingController::class, 'index']);
Route::get('/mcp', [McpController::class, 'index']);
