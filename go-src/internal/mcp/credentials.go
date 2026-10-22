// internal/mcp/credentials.go
//
// MCP 凭据（API Key）与「凭据 × 工具」权限矩阵。
//
// 权限模型对齐宝塔 MCP：
//   - 每个凭据独立维护已授权工具集 ToolIDs，其余归入 Disabled（两者构成互斥分区）
//   - 低风险工具默认授权；中/高风险默认不授权（opt-in，需在后台显式勾选）
//   - 显式禁用的工具永不自动恢复
//   - 凭据支持启停与有效期（-1 永不过期）
//
// 凭据由 PHP 后台维护并随配置下发（LocalServiceConfig.MCP），Go 侧只读使用，
// 不做本地持久化——保证「配置真源在 PHP 后台」这一单一事实来源。
package mcp

import (
	"crypto/subtle"
	"strings"
	"time"
)

// Credential MCP 访问凭据
type Credential struct {
	ID          string   `json:"id"`
	Token       string   `json:"token"`
	Name        string   `json:"name"`
	ToolIDs     []string `json:"tool_ids"`     // 已授权工具
	Disabled    []string `json:"disabled"`     // 显式禁用工具
	CreatedAt   string   `json:"created_at"`   // YYYY-MM-DD HH:MM:SS
	ExpiresDays int      `json:"expires_days"` // -1 永不过期；N 表示创建后 N 天失效
	Active      bool     `json:"active"`
}

// IsExpired 是否已过期（创建时间无法解析时按失效处理，安全兜底）
func (c *Credential) IsExpired(now time.Time) bool {
	if c.ExpiresDays < 0 {
		return false
	}
	created, err := time.ParseInLocation("2006-01-02 15:04:05", c.CreatedAt, time.Local)
	if err != nil {
		return true
	}
	return now.After(created.AddDate(0, 0, c.ExpiresDays))
}

// EffectiveTools 解析该凭据当前实际可调用的工具集（与当前注册工具求交）
func (c *Credential) EffectiveTools(registered []string) map[string]bool {
	registeredSet := make(map[string]bool, len(registered))
	for _, id := range registered {
		registeredSet[id] = true
	}

	granted := make(map[string]bool, len(c.ToolIDs))
	for _, id := range c.ToolIDs {
		if registeredSet[id] {
			granted[id] = true
		}
	}

	// 显式禁用优先（双保险：即使同时出现在 ToolIDs 中也剔除）
	for _, id := range c.Disabled {
		delete(granted, id)
	}
	return granted
}

// CredentialStore 凭据集合（由配置下发，进程内只读快照）
type CredentialStore struct {
	keys []*Credential
}

// NewCredentialStore 构建凭据集合（深拷贝入参，避免调用方后续修改影响运行时）
func NewCredentialStore(keys []*Credential) *CredentialStore {
	copied := make([]*Credential, 0, len(keys))
	for _, k := range keys {
		if k == nil || k.Token == "" {
			continue
		}
		copied = append(copied, &Credential{
			ID:          k.ID,
			Token:       k.Token,
			Name:        k.Name,
			ToolIDs:     append([]string(nil), k.ToolIDs...),
			Disabled:    append([]string(nil), k.Disabled...),
			CreatedAt:   k.CreatedAt,
			ExpiresDays: k.ExpiresDays,
			Active:      k.Active,
		})
	}
	return &CredentialStore{keys: copied}
}

// FindByToken 按 Token 查找凭据（常量时间比较，避免时序侧信道）
func (s *CredentialStore) FindByToken(token string) *Credential {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	for _, k := range s.keys {
		if subtle.ConstantTimeCompare([]byte(k.Token), []byte(token)) == 1 {
			return k
		}
	}
	return nil
}

// Keys 返回全部凭据（后台展示用）
func (s *CredentialStore) Keys() []*Credential {
	return s.keys
}

// Count 凭据数量
func (s *CredentialStore) Count() int {
	return len(s.keys)
}

// DefaultToolIDs 为新建凭据计算默认授权集：全部低风险工具
func DefaultToolIDs(reg *Registry) []string {
	return reg.LowRiskIDs()
}

// NormalizeToolIDs 规范化授权集：按注册顺序排序、去重，并剔除未注册项。
//
// 与 disabled 一起构成互斥分区：传入的 toolIDs 视为「应授权集」，
// 返回值仅保留已注册项，其余由 Disabled 承载。
func NormalizeToolIDs(toolIDs []string, registered []string) []string {
	want := make(map[string]bool, len(toolIDs))
	for _, id := range toolIDs {
		want[id] = true
	}
	out := make([]string, 0, len(toolIDs))
	for _, id := range registered {
		if want[id] {
			out = append(out, id)
		}
	}
	return out
}

// ComplementToolIDs 计算补集（registered - granted），即 Disabled
func ComplementToolIDs(granted []string, registered []string) []string {
	grantedSet := make(map[string]bool, len(granted))
	for _, id := range granted {
		grantedSet[id] = true
	}
	out := make([]string, 0)
	for _, id := range registered {
		if !grantedSet[id] {
			out = append(out, id)
		}
	}
	return out
}
