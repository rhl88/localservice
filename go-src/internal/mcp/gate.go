// internal/mcp/gate.go
//
// 请求门控：二次确认闸、IP 白名单、滑动窗口限流。
//
// 三者独立可测，均不依赖 HTTP 层，便于单元测试直接验证。
package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"time"
)

// ConfirmTTL 确认记录有效期（秒）：超时后需重新发起确认
const ConfirmTTL = 600

// ConfirmInstruction 首次调用（confirm=false）时返回给 AI 的指令文案
const ConfirmInstruction = "本次工具调用尚未执行。请立即停止调用工具，根据当前对话、工具说明和刚才提交的参数，" +
	"向用户说明将要执行的操作、目标及可能影响，并明确询问是否继续。只有收到用户后续消息明确同意后，" +
	"才能使用完全相同的业务参数并设置 confirm=true。禁止在当前轮次自行确认。"

// ConfirmDescription 注入到需要确认的工具 schema 中的 confirm 参数说明
const ConfirmDescription = "首次调用必须为 false，仅建立确认记录且不会执行。收到 waiting 状态后必须停止并询问用户。" +
	"只有收到一条后续用户消息明确同意，才可使用完全相同的业务参数设置为 true。"

// ConfirmGate 进程内确认记录（带 TTL，确认时原子消费以防并发/重试双执行）
type ConfirmGate struct {
	ttl   time.Duration
	mu    sync.Mutex
	items map[string]time.Time
	now   func() time.Time // 便于测试注入
}

// NewConfirmGate 创建确认闸
func NewConfirmGate(ttl time.Duration) *ConfirmGate {
	if ttl <= 0 {
		ttl = ConfirmTTL * time.Second
	}
	return &ConfirmGate{
		ttl:   ttl,
		items: make(map[string]time.Time),
		now:   time.Now,
	}
}

// confirmKey 确认键：凭据 + 工具 + 参数（完整一致才算同一确认请求）
func confirmKey(credentialID, toolID string, args map[string]interface{}) string {
	payload := map[string]interface{}{
		"credential": credentialID,
		"tool":       toolID,
		"args":       args,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte(credentialID + "|" + toolID)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Check 校验确认状态。
//
// 返回值：
//   - ("waiting", "")          首次调用，已建立确认记录，尚未执行
//   - ("confirmed", "")        用户已确认且参数一致，放行执行
//   - ("rejected", 原因)       未先确认就直接放行 / 记录过期
func (g *ConfirmGate) Check(credentialID, toolID string, args map[string]interface{}, ok bool) (string, string) {
	key := confirmKey(credentialID, toolID, args)

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	g.prune(now)

	if !ok {
		// 首次调用：记录待确认，不执行
		g.items[key] = now
		return "waiting", ""
	}

	created, exists := g.items[key]
	if !exists {
		return "rejected", "请先调用本工具生成确认请求，并在用户明确同意后再设置 confirm=true"
	}
	// 原子消费：确认记录一次性使用，防重放
	delete(g.items, key)

	if now.Sub(created) > g.ttl {
		return "rejected", "确认记录已过期，请重新发起确认"
	}
	return "confirmed", ""
}

// prune 清理过期记录（调用方须持锁）
func (g *ConfirmGate) prune(now time.Time) {
	for key, created := range g.items {
		if now.Sub(created) > g.ttl {
			delete(g.items, key)
		}
	}
}

// PendingCount 当前待确认记录数（测试与观测用）
func (g *ConfirmGate) PendingCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prune(g.now())
	return len(g.items)
}

// ============ IP 白名单 ============

// IPAllowlist IP 白名单：支持 "*"（放行全部）、单 IP、CIDR、IP 区间 A-B。
//
// 语义对齐宝塔：`*` 放行全部；空列表拦截所有（安全默认）；非空仅放行命中项。
type IPAllowlist struct {
	allowAll bool
	nets     []*net.IPNet
	ranges   [][2]net.IP
}

// NewIPAllowlist 构建白名单
func NewIPAllowlist(entries []string) *IPAllowlist {
	a := &IPAllowlist{}
	a.apply(entries)
	return a
}

func (a *IPAllowlist) apply(entries []string) {
	a.allowAll = false
	a.nets = nil
	a.ranges = nil

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			a.allowAll = true
			continue
		}
		if strings.Contains(entry, "-") {
			parts := strings.SplitN(entry, "-", 2)
			start := net.ParseIP(strings.TrimSpace(parts[0]))
			end := net.ParseIP(strings.TrimSpace(parts[1]))
			if start == nil || end == nil {
				continue
			}
			// 仅接受同族且 start <= end
			if (start.To4() == nil) != (end.To4() == nil) {
				continue
			}
			if compareIP(start, end) > 0 {
				continue
			}
			a.ranges = append(a.ranges, [2]net.IP{start, end})
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			a.nets = append(a.nets, network)
			continue
		}
		// 单个 IP 按 /32 或 /128 处理
		if ip := net.ParseIP(entry); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
				ip = ip.To4()
			}
			a.nets = append(a.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
}

// IsAllowed 判断客户端 IP 是否放行
func (a *IPAllowlist) IsAllowed(clientIP string) bool {
	if a.allowAll {
		return true
	}
	if len(a.nets) == 0 && len(a.ranges) == 0 {
		return false // 空列表拦截所有
	}

	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	for _, network := range a.nets {
		if network.Contains(ip) {
			return true
		}
	}
	for _, r := range a.ranges {
		if compareIP(r[0], ip) <= 0 && compareIP(ip, r[1]) <= 0 {
			return true
		}
	}
	return false
}

// AllowAll 是否放行全部
func (a *IPAllowlist) AllowAll() bool { return a.allowAll }

// compareIP 比较两个同族 IP（-1/0/1）
func compareIP(a, b net.IP) int {
	if v4a, v4b := a.To4(), b.To4(); v4a != nil && v4b != nil {
		a, b = v4a, v4b
	}
	na, nb := a, b
	for i := 0; i < len(na) && i < len(nb); i++ {
		if na[i] < nb[i] {
			return -1
		}
		if na[i] > nb[i] {
			return 1
		}
	}
	return 0
}

// ValidateAllowEntry 校验白名单条目合法性（后台保存时用）
func ValidateAllowEntry(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return errInvalid("IP 不能为空")
	}
	if entry == "*" {
		return nil
	}
	test := NewIPAllowlist([]string{entry})
	if !test.allowAll && len(test.nets) == 0 && len(test.ranges) == 0 {
		return errInvalid("IP 格式无效（支持 IP、CIDR 或 A-B 区间）")
	}
	return nil
}

type errInvalid string

func (e errInvalid) Error() string { return string(e) }

// ============ 滑动窗口限流 ============

// RateLimiter 按 key（凭据标识 + IP）滑动窗口限流
type RateLimiter struct {
	max    int
	window time.Duration
	mu     sync.Mutex
	hits   map[string][]time.Time
	now    func() time.Time
}

// NewRateLimiter 创建限流器（max<=0 时回退 300 次 / 60 秒）
func NewRateLimiter(max, windowSeconds int) *RateLimiter {
	if max <= 0 {
		max = 300
	}
	if windowSeconds <= 0 {
		windowSeconds = 60
	}
	return &RateLimiter{
		max:    max,
		window: time.Duration(windowSeconds) * time.Second,
		hits:   make(map[string][]time.Time),
		now:    time.Now,
	}
}

// Allow 记录一次请求并返回是否放行
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)

	bucket := l.hits[key]
	kept := bucket[:0]
	for _, t := range bucket {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// Max 当前阈值
func (l *RateLimiter) Max() int { return l.max }

// WindowSeconds 当前窗口秒数
func (l *RateLimiter) WindowSeconds() int { return int(l.window / time.Second) }
