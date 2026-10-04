// Package quota 管理多租户的刷新/预热日配额与按日用量。
package quota

import (
	"errors"
	"sync"
)

const secondsDay = 86400

var (
	// ErrInvalidTenant 租户名为空。
	ErrInvalidTenant = errors.New("quota: invalid tenant")
	// ErrInvalidQuota 配额越界（0..1e6）。
	ErrInvalidQuota = errors.New("quota: invalid quota")
	// ErrUnknownTenant 租户未注册。
	ErrUnknownTenant = errors.New("quota: unknown tenant")
	// ErrDuplicateTenant 重复注册。
	ErrDuplicateTenant = errors.New("quota: duplicate tenant")
	// ErrExceeded 追加后将超过某项配额。
	ErrExceeded = errors.New("quota: quota exceeded")
)

// 配额类别索引，用于 [3]int 数组。
const (
	IdxURL = iota
	IdxDir
	IdxWarm
)

// Limits 为一个租户的三项日配额。
type Limits struct {
	Qu int // URL 刷新条数
	Qd int // 目录刷新条数
	Qw int // 预热条数
}

// Registry 保存全部租户的配额与按日已用量，并发安全。
type Registry struct {
	mu      sync.RWMutex
	tenants map[string]*tenant
}

type tenant struct {
	limits Limits
	day    int64
	used   [3]int
}

// NewRegistry 创建空租户注册表。
func NewRegistry() *Registry {
	return &Registry{tenants: map[string]*tenant{}}
}

// Register 注册租户；重复注册返回错误。
func (r *Registry) Register(name string, l Limits) error {
	if name == "" {
		return ErrInvalidTenant
	}
	if l.Qu < 0 || l.Qu > 1_000_000 || l.Qd < 0 || l.Qd > 1_000_000 || l.Qw < 0 || l.Qw > 1_000_000 {
		return ErrInvalidQuota
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tenants[name]; ok {
		return ErrDuplicateTenant
	}
	r.tenants[name] = &tenant{limits: l, day: -1}
	return nil
}

// Has 报告租户是否已注册。
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tenants[name]
	return ok
}

// Limits 返回租户配额；第二个返回值报告是否存在。
func (r *Registry) Limits(name string) (Limits, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tenants[name]
	if !ok {
		return Limits{}, false
	}
	return t.limits, true
}

// roll 跨日惰性归零，调用方持锁。
func (t *tenant) roll(now int64) {
	day := now / secondsDay
	if t.day != day {
		t.day = day
		t.used = [3]int{}
	}
}

// Used 返回某租户在 now 所在日的三项已用量（跨日惰性归零）。
func (r *Registry) Used(name string, now int64) (u, d, w int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tenants[name]
	if !ok {
		return 0, 0, 0, ErrUnknownTenant
	}
	t.roll(now)
	return t.used[IdxURL], t.used[IdxDir], t.used[IdxWarm], nil
}

// Check 仅校验在 now 所在日追加 add 是否可行，不修改状态。
func (r *Registry) Check(name string, now int64, add [3]int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tenants[name]
	if !ok {
		return ErrUnknownTenant
	}
	t.roll(now)
	for i := 0; i < 3; i++ {
		if t.used[i]+add[i] > t.limitsIndex(i) {
			return ErrExceeded
		}
	}
	return nil
}

func (t *tenant) limitsIndex(i int) int {
	switch i {
	case IdxURL:
		return t.limits.Qu
	case IdxDir:
		return t.limits.Qd
	default:
		return t.limits.Qw
	}
}

// Apply 在已通过 Check 的前提下记账（调用方须保证原子语义）。
func (r *Registry) Apply(name string, now int64, add [3]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tenants[name]
	if !ok {
		return
	}
	t.roll(now)
	for i := 0; i < 3; i++ {
		t.used[i] += add[i]
	}
}
