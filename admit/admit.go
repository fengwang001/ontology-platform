// Package admit 负责入队策略与每租户背压。
package admit

import "errors"

// 四类可通过 errors.Is 区分的入队/管理错误。
var (
	// ErrBadWeight 权重为 0 或负。
	ErrBadWeight = errors.New("admit: weight must be positive")
	// ErrQueueFull 超过该租户队列上限。
	ErrQueueFull = errors.New("admit: tenant queue full")
	// ErrNoSuchTenant 租户未注册或已移除。
	ErrNoSuchTenant = errors.New("admit: no such tenant")
	// ErrQueueNotEmpty 移除仍有排队任务的租户。
	ErrQueueNotEmpty = errors.New("admit: tenant queue not empty")
)

// Manager 记录每个租户的队列容量并做出入队裁决（非并发安全）。
// cap<=0 表示不限容量。
type Manager struct {
	caps map[string]int
}

// New 创建空的入队管理器。
func New() *Manager { return &Manager{caps: map[string]int{}} }

// Register 记录租户容量；重复注册更新容量。
func (m *Manager) Register(id string, cap int) { m.caps[id] = cap }

// Forget 删除租户记录。
func (m *Manager) Forget(id string) { delete(m.caps, id) }

// Known 报告租户是否已注册。
func (m *Manager) Known(id string) bool {
	_, ok := m.caps[id]
	return ok
}

// Allow 判定 queued 个在队任务的租户能否再入队一个。
// 未注册返回 ErrNoSuchTenant；超限返回 ErrQueueFull（拒绝不影响其他租户）。
func (m *Manager) Allow(id string, queued int) error {
	cap, ok := m.caps[id]
	if !ok {
		return ErrNoSuchTenant
	}
	if cap > 0 && queued >= cap {
		return ErrQueueFull
	}
	return nil
}
