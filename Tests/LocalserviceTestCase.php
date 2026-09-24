<?php

namespace App\Apps\CmsproLocalservice\Tests;

use App\Enums\Status;
use App\Models\AdminRole;
use App\Models\AdminUser;
use Illuminate\Foundation\Testing\RefreshDatabase;
use Tests\TestCase;

/**
 * CmsproLocalservice 应用 · 测试基类
 *
 * 说明：
 * 1. 应用迁移经 Install::install() 幂等建表并记录 migrations 表
 * 2. 应用 ServiceProvider 仅对已安装应用注册，测试环境需手动注册以加载路由
 */
abstract class LocalserviceTestCase extends TestCase
{
    use RefreshDatabase;

    protected function setUp(): void
    {
        parent::setUp();

        (new \App\Apps\CmsproLocalservice\Install())->install();
        $this->registerAppProvider();
    }

    /**
     * 手动注册应用 ServiceProvider（加载路由前缀 admin/cmspro/localservice 与 api/admin/cmspro/localservice）
     */
    protected function registerAppProvider(): void
    {
        $provider = $this->app->register(\App\Apps\CmsproLocalservice\ServiceProvider::class);
        if ($provider && method_exists($provider, 'boot')) {
            $this->app->call([$provider, 'boot']);
        }
    }

    /**
     * 构造并登录一个超管管理员（super_admin 角色放行全部权限）
     */
    protected function actingAdmin(bool $login = true): AdminUser
    {
        $admin = AdminUser::create([
            'username' => 'admin_' . uniqid(),
            'password' => bcrypt('123456'),
            'name'     => '测试管理员',
            'status'   => 1,
        ]);

        $role = AdminRole::firstOrCreate(
            ['code' => 'super_admin'],
            [
                'name'        => '超级管理员',
                'description' => '系统内置超级管理员',
                'parent_id'   => 0,
                'sort'        => 0,
                'status'      => Status::ENABLED,
            ]
        );
        $admin->roles()->attach($role->id);

        if ($login) {
            $this->actingAs($admin, 'admin');
        }

        return $admin;
    }
}
