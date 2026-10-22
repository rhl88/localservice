package mcp

import (
	"testing"
	"time"
)

// ============ 工具注册表 ============

func TestRegisterBuiltinTools(t *testing.T) {
	reg := &Registry{tools: make(map[string]*Tool)}
	if err := RegisterBuiltinTools(reg); err != nil {
		t.Fatalf("注册内置工具失败: %v", err)
	}
	if reg.Count() == 0 {
		t.Fatal("内置工具注册后数量为 0")
	}

	// 每个工具都应有 task_type、合法风险等级与分类
	for _, tool := range reg.List() {
		if tool.TaskType == "" {
			t.Errorf("工具 %s 缺少 task_type", tool.ToolID)
		}
		switch tool.RiskLevel {
		case RiskLow, RiskMedium, RiskHigh:
		default:
			t.Errorf("工具 %s 风险等级非法: %s", tool.ToolID, tool.RiskLevel)
		}
		if tool.Category == "" {
			t.Errorf("工具 %s 缺少分类", tool.ToolID)
		}
		if tool.Description == "" {
			t.Errorf("工具 %s 缺少描述", tool.ToolID)
		}
	}
}

func TestRegisterDuplicateToolID(t *testing.T) {
	reg := &Registry{tools: make(map[string]*Tool)}
	tool := &Tool{ToolID: "Foo", TaskType: "foo.bar", RiskLevel: RiskLow}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("首次注册应成功: %v", err)
	}
	if err := reg.Register(tool); err == nil {
		t.Fatal("重复 tool_id 应报错")
	}
}

func TestRegisterRejectsInvalidRiskLevel(t *testing.T) {
	reg := &Registry{tools: make(map[string]*Tool)}
	err := reg.Register(&Tool{ToolID: "Bad", TaskType: "bad", RiskLevel: "critical"})
	if err == nil {
		t.Fatal("非法风险等级应报错")
	}
}

func TestRegisterRejectsMissingTaskType(t *testing.T) {
	reg := &Registry{tools: make(map[string]*Tool)}
	if err := reg.Register(&Tool{ToolID: "NoTask", RiskLevel: RiskLow}); err == nil {
		t.Fatal("缺少 task_type 应报错")
	}
}

func TestLowRiskIDs(t *testing.T) {
	reg := &Registry{tools: make(map[string]*Tool)}
	if err := RegisterBuiltinTools(reg); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	lows := reg.LowRiskIDs()
	if len(lows) == 0 {
		t.Fatal("应存在低风险工具")
	}
	for _, id := range lows {
		tool, _ := reg.Get(id)
		if tool.RiskLevel != RiskLow {
			t.Errorf("工具 %s 不应出现在低风险列表", id)
		}
	}
}

func TestRiskCounts(t *testing.T) {
	reg := &Registry{tools: make(map[string]*Tool)}
	if err := RegisterBuiltinTools(reg); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	counts := reg.RiskCounts()
	total := counts[RiskLow] + counts[RiskMedium] + counts[RiskHigh]
	if total != reg.Count() {
		t.Errorf("风险统计合计 %d 与工具总数 %d 不一致", total, reg.Count())
	}
}

func TestHighRiskToolsRequireConfirm(t *testing.T) {
	reg := &Registry{tools: make(map[string]*Tool)}
	if err := RegisterBuiltinTools(reg); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	// 高风险工具必须声明 confirm，避免破坏性操作被直接执行
	for _, tool := range reg.List() {
		if tool.RiskLevel == RiskHigh && !tool.Confirm {
			t.Errorf("高风险工具 %s 未声明 confirm", tool.ToolID)
		}
	}
}

func TestMetaListSortedAndFallback(t *testing.T) {
	reg := &Registry{tools: make(map[string]*Tool)}
	if err := RegisterBuiltinTools(reg); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	metas := reg.MetaList()
	if len(metas) != reg.Count() {
		t.Fatalf("元数据数量 %d 与工具数 %d 不一致", len(metas), reg.Count())
	}
	for i, m := range metas {
		if m.Name == "" {
			t.Errorf("第 %d 项缺少展示名", i)
		}
		if m.Category == "" {
			t.Errorf("第 %d 项缺少分类", i)
		}
		if i > 0 && metas[i-1].Category == m.Category && metas[i-1].ToolID > m.ToolID {
			t.Errorf("同分类内未按 tool_id 排序: %s > %s", metas[i-1].ToolID, m.ToolID)
		}
	}
}

// ============ 确认门 ============

func TestConfirmGateWaitingThenConfirmed(t *testing.T) {
	gate := NewConfirmGate(time.Minute)
	args := map[string]interface{}{"path": "/tmp/a.txt"}

	// 首次调用（confirm=false）：建立记录，不执行
	state, _ := gate.Check("cred1", "FileDelete", args, false)
	if state != "waiting" {
		t.Fatalf("首次调用应返回 waiting，实际 %s", state)
	}
	if gate.PendingCount() != 1 {
		t.Fatalf("应有 1 条待确认记录，实际 %d", gate.PendingCount())
	}

	// 用户确认后（confirm=true，参数一致）：放行
	state, _ = gate.Check("cred1", "FileDelete", args, true)
	if state != "confirmed" {
		t.Fatalf("参数一致时应放行，实际 %s", state)
	}
	// 确认记录应被消费（防重放）
	if gate.PendingCount() != 0 {
		t.Fatalf("确认后记录应被消费，实际剩余 %d", gate.PendingCount())
	}
}

func TestConfirmGateRejectsWithoutPriorRequest(t *testing.T) {
	gate := NewConfirmGate(time.Minute)
	state, msg := gate.Check("cred1", "SystemExec", map[string]interface{}{"command": "ls"}, true)
	if state != "rejected" {
		t.Fatalf("未先发起确认就放行应被拒绝，实际 %s", state)
	}
	if msg == "" {
		t.Fatal("拒绝时应返回原因")
	}
}

func TestConfirmGateRejectsDifferentArgs(t *testing.T) {
	gate := NewConfirmGate(time.Minute)
	gate.Check("cred1", "FileWrite", map[string]interface{}{"path": "/tmp/a", "content": "x"}, false)

	// 参数不同 → 视为新的确认请求（应 waiting 而非 confirmed）
	state, _ := gate.Check("cred1", "FileWrite", map[string]interface{}{"path": "/tmp/b", "content": "x"}, true)
	if state != "rejected" {
		t.Fatalf("参数不一致应被拒绝，实际 %s", state)
	}
}

func TestConfirmGateIsolatedPerCredential(t *testing.T) {
	gate := NewConfirmGate(time.Minute)
	args := map[string]interface{}{"name": "nginx"}
	gate.Check("credA", "ServiceStop", args, false)

	// 另一凭据不应享受 credA 的确认
	state, _ := gate.Check("credB", "ServiceStop", args, true)
	if state != "rejected" {
		t.Fatalf("确认应隔离到凭据，实际 %s", state)
	}
}

func TestConfirmGateExpiry(t *testing.T) {
	gate := NewConfirmGate(time.Minute)
	current := time.Now()
	gate.now = func() time.Time { return current }

	args := map[string]interface{}{"pid": 123}
	gate.Check("cred1", "ProcessKill", args, false)

	// 时间推进超过 TTL
	gate.now = func() time.Time { return current.Add(2 * time.Minute) }
	state, msg := gate.Check("cred1", "ProcessKill", args, true)
	if state != "rejected" {
		t.Fatalf("过期确认应被拒绝，实际 %s", state)
	}
	if msg == "" {
		t.Fatal("过期应返回提示")
	}
}

func TestConfirmGateUnableToSerializeArgs(t *testing.T) {
	gate := NewConfirmGate(time.Minute)
	// 含无法序列化的值（channel）时走兜底键，不应 panic
	args := map[string]interface{}{"bad": make(chan int)}
	state, _ := gate.Check("cred1", "SystemExec", args, false)
	if state != "waiting" {
		t.Fatalf("无法序列化时应走兜底键并返回 waiting，实际 %s", state)
	}
}

// ============ IP 白名单 ============

func TestIPAllowlistStarAllowsAll(t *testing.T) {
	a := NewIPAllowlist([]string{"*"})
	if !a.IsAllowed("1.2.3.4") {
		t.Fatal("* 应放行全部")
	}
}

func TestIPAllowlistEmptyDeniesAll(t *testing.T) {
	a := NewIPAllowlist([]string{})
	if a.IsAllowed("127.0.0.1") {
		t.Fatal("空列表应拦截所有（安全默认）")
	}
}

func TestIPAllowlistSingleAndCIDR(t *testing.T) {
	a := NewIPAllowlist([]string{"192.168.1.10", "10.0.0.0/8"})
	if !a.IsAllowed("192.168.1.10") {
		t.Error("单 IP 应放行")
	}
	if a.IsAllowed("192.168.1.11") {
		t.Error("非白名单单 IP 应拦截")
	}
	if !a.IsAllowed("10.5.6.7") {
		t.Error("CIDR 内应放行")
	}
	if a.IsAllowed("11.0.0.1") {
		t.Error("CIDR 外应拦截")
	}
}

func TestIPAllowlistRange(t *testing.T) {
	a := NewIPAllowlist([]string{"192.168.1.10-192.168.1.20"})
	if !a.IsAllowed("192.168.1.15") {
		t.Error("区间内应放行")
	}
	if a.IsAllowed("192.168.1.21") {
		t.Error("区间外应拦截")
	}
}

func TestIPAllowlistIPv6(t *testing.T) {
	a := NewIPAllowlist([]string{"::1", "2001:db8::/32"})
	if !a.IsAllowed("::1") {
		t.Error("IPv6 回环应放行")
	}
	if !a.IsAllowed("2001:db8::5") {
		t.Error("IPv6 CIDR 内应放行")
	}
	if a.IsAllowed("2001:db9::5") {
		t.Error("IPv6 CIDR 外应拦截")
	}
}

func TestIPAllowlistInvalidIPDenied(t *testing.T) {
	a := NewIPAllowlist([]string{"192.168.1.1"})
	if a.IsAllowed("not-an-ip") {
		t.Error("非法 IP 应拦截")
	}
}

func TestValidateAllowEntry(t *testing.T) {
	valid := []string{"*", "127.0.0.1", "10.0.0.0/8", "192.168.1.1-192.168.1.50", "::1"}
	for _, e := range valid {
		if err := ValidateAllowEntry(e); err != nil {
			t.Errorf("条目 %s 应合法，实际报错: %v", e, err)
		}
	}
	invalid := []string{"", "  ", "abc", "999.999.999.999", "10.0.0.0/99"}
	for _, e := range invalid {
		if err := ValidateAllowEntry(e); err == nil {
			t.Errorf("条目 %q 应非法", e)
		}
	}
}

// ============ 限流 ============

func TestRateLimiterAllowsUpToMax(t *testing.T) {
	l := NewRateLimiter(3, 60)
	for i := 0; i < 3; i++ {
		if !l.Allow("k1") {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if l.Allow("k1") {
		t.Fatal("超过上限应拒绝")
	}
}

func TestRateLimiterIsolatedPerKey(t *testing.T) {
	l := NewRateLimiter(2, 60)
	l.Allow("k1")
	l.Allow("k1")
	if l.Allow("k1") {
		t.Fatal("k1 应已达上限")
	}
	if !l.Allow("k2") {
		t.Fatal("k2 不应受 k1 影响")
	}
}

func TestRateLimiterWindowSlides(t *testing.T) {
	l := NewRateLimiter(2, 1)
	current := time.Now()
	l.now = func() time.Time { return current }

	l.Allow("k")
	l.Allow("k")
	if l.Allow("k") {
		t.Fatal("窗口内应已超限")
	}

	// 时间推进超出窗口后应恢复
	l.now = func() time.Time { return current.Add(2 * time.Second) }
	if !l.Allow("k") {
		t.Fatal("超出窗口后应恢复放行")
	}
}

func TestRateLimiterDefaults(t *testing.T) {
	l := NewRateLimiter(0, 0)
	if l.Max() != 300 {
		t.Errorf("默认上限应为 300，实际 %d", l.Max())
	}
	if l.WindowSeconds() != 60 {
		t.Errorf("默认窗口应为 60 秒，实际 %d", l.WindowSeconds())
	}
}

// ============ 结果体积限制 ============

func TestFitResultPassesSmallResult(t *testing.T) {
	res := Ok("done", map[string]interface{}{"v": 1})
	got := FitResult(res)
	if !got.Status {
		t.Fatal("小结果应原样返回")
	}
}

func TestFitResultRejectsOversize(t *testing.T) {
	big := make([]byte, MaxResultBytes+1024)
	for i := range big {
		big[i] = 'a'
	}
	res := Ok("done", map[string]interface{}{"blob": string(big)})
	got := FitResult(res)
	if got.Status {
		t.Fatal("超限结果应判失败")
	}
	if got.Msg == "" {
		t.Fatal("超限应给出提示")
	}
}

func TestFitResultNil(t *testing.T) {
	got := FitResult(nil)
	if got == nil || got.Status {
		t.Fatal("nil 结果应返回失败结构")
	}
}
