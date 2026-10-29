<?php

namespace App\Apps\CmsproLocalservice\Tests\Feature;

use App\Apps\CmsproLocalservice\Tests\LocalserviceTestCase;
use Illuminate\Http\Client\ConnectionException;
use Illuminate\Support\Facades\Http;

/**
 * 桌面小工具数据接口（GET /api/admin/cmspro/localservice/widget/status）
 *
 * WebOS 桌面小工具按 manifest.widgets[].refresh 秒轮询该接口，统一返回契约：
 * {title,status,value,unit,items[{label,value}],updated_at}，status 取 running|stopped|warning。
 */
class WidgetStatusTest extends LocalserviceTestCase
{
    public function test_requires_admin_login(): void
    {
        $this->getJson('/api/admin/cmspro/localservice/widget/status')->assertStatus(401);
    }

    public function test_returns_running_contract_when_service_online(): void
    {
        $this->actingAdmin();

        Http::fake(function ($request) {
            $url = $request->url();
            if (str_contains($url, '/api/status')) {
                return Http::response(['code' => 0, 'data' => ['version' => '9.9.9']]);
            }
            // 累计任务与成功任务两次 listTasks 以是否带 status=success 区分
            $total = str_contains($url, 'status=success') ? 8 : 10;

            return Http::response(['code' => 0, 'data' => ['total' => $total]]);
        });

        $response = $this->getJson('/api/admin/cmspro/localservice/widget/status')
            ->assertOk()
            ->assertJsonPath('code', 0)
            ->assertJsonPath('data.status', 'running')
            ->assertJsonPath('data.value', '9.9.9')
            ->assertJsonPath('data.items.0.label', '端口')
            ->assertJsonPath('data.items.2.value', '80%');

        $data = $response->json('data');

        $this->assertSame('本地服务状态', $data['title']);
        $this->assertCount(3, $data['items']);
        $this->assertMatchesRegularExpression('/^\d+$/', (string) $data['items'][0]['value']);
        $this->assertMatchesRegularExpression('/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/', (string) $data['updated_at']);
    }

    public function test_marks_offline_service_stopped_or_warning(): void
    {
        $this->actingAdmin();

        Http::fake(function () {
            throw new ConnectionException('connection refused');
        });

        $response = $this->getJson('/api/admin/cmspro/localservice/widget/status')
            ->assertOk()
            ->assertJsonPath('data.items.2.value', '—');

        $this->assertContains($response->json('data.status'), ['stopped', 'warning']);
    }
}