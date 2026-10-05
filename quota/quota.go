// Package quota 实现多租户控制面的按日配额计量与共享时钟。
//
// Meter 的互斥锁是整个控制面唯一的串行化点：purge 与 edge 的每个
// 操作都在持有 Meter 锁的临界区内调用本包的其余方法，因此所有并发
// 操作的结果都等价于某个串行顺序。Meter 的各方法本身不再加锁。
package quota

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("quota: invalid argument")
	ErrClockBackward   = errors.New("quota: clock regression")
	ErrTenantNotFound  = errors.New("quota: tenant not found")
	ErrURLQuota        = errors.New("quota: url refresh quota exceeded")
	ErrDirQuota        = errors.New("quota: dir refresh quota exceeded")
	ErrPrewarmQuota    = errors.New("quota: prewarm quota exceeded")
)

const (
	// MaxNow 是 now 的最大合法值（10^12 秒）。
	MaxNow int64 = 1_000_000_000_000
	// MaxQuota 是每项日配额的最大合法值（10^6）。
	MaxQuota uint64 = 1_000_000
	// DaySeconds 是一日的秒数。
	DaySeconds int64 = 86400
)

// Kind 标识三项日配额之一。
type Kind int

const (
	KindURL     Kind = iota // URL 刷新条数
	KindDir                 // 目录刷新条数
	KindPrewarm             // 预热条数
)

// Quotas 是一个租户的三项日配额。
type Quotas struct {
	URL     uint64
	Dir     uint64
	Prewarm uint64
}

func (q Quotas) valid() bool {
	return q.URL <= MaxQuota && q.Dir <= MaxQuota && q.Prewarm <= MaxQuota
}

// ValidNow 报告 now 是否处于合法域 [0, MaxNow]。
func ValidNow(now int64) bool { return now >= 0 && now <= MaxNow }

// DayOf 返回 now 所属的日号（now 除以 86400 向下取整）。
func DayOf(now int64) int64 { return now / DaySeconds }

type tenantState struct {
	quotas Quotas
	day    int64
	used   [3]uint64
}

// Meter 是配额计量器，同时持有控制面共享时钟与全局串行锁。
type Meter struct {
	mu      sync.Mutex
	maxNow  int64
	tenants map[string]*tenantState
}

// NewMeter 返回一个空计量器。
func NewMeter() *Meter { return &Meter{tenants: make(map[string]*tenantState)} }

// Lock 进入控制面临界区。
func (m *Meter) Lock() { m.mu.Lock() }

// Unlock 离开控制面临界区。
func (m *Meter) Unlock() { m.mu.Unlock() }

// Register 注册租户并设置三项日配额，仅在服务启动前调用。
func (m *Meter) Register(id string, q Quotas) error {
	if id == "" || !q.valid() {
		return ErrInvalidArgument
	}
	m.tenants[id] = &tenantState{quotas: q, day: -1}
	return nil
}

// MaxNow 返回已接受操作的最大 now。
func (m *Meter) MaxNow() int64 { return m.maxNow }

// Used 返回租户在日号 DayOf(now) 内 kind 项的已用量（now 的纯函数）。
func (m *Meter) Used(now int64, id string, k Kind) uint64 {
	t := m.tenants[id]
	if t == nil || t.day != DayOf(now) {
		return 0
	}
	return t.used[k]
}

func (m *Meter) checkClock(now int64) error {
	if now < m.maxNow {
		return ErrClockBackward
	}
	return nil
}

// tenant 查找租户，并按需把已用量滚动到 now 所属日号。
// 滚动是 now 的纯函数，不依赖是否有操作触发。
func (m *Meter) tenant(id string, now int64) (*tenantState, error) {
	t := m.tenants[id]
	if t == nil {
		return nil, ErrTenantNotFound
	}
	if d := DayOf(now); t.day != d {
		t.day = d
		t.used = [3]uint64{}
	}
	return t, nil
}

// ChargePurge 依次检查时钟、租户、URL 配额、目录配额；全部通过才计费
// 并推进时钟，任一不足则整批拒绝且已用量与时钟都不变。
func (m *Meter) ChargePurge(now int64, id string, u, d uint64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	t, err := m.tenant(id, now)
	if err != nil {
		return err
	}
	if t.used[KindURL]+u > t.quotas.URL {
		return ErrURLQuota
	}
	if t.used[KindDir]+d > t.quotas.Dir {
		return ErrDirQuota
	}
	t.used[KindURL] += u
	t.used[KindDir] += d
	m.maxNow = now
	return nil
}

// CheckPrewarm 依次检查时钟、租户、预热配额，不改变任何可观测状态。
func (m *Meter) CheckPrewarm(now int64, id string, w uint64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	t, err := m.tenant(id, now)
	if err != nil {
		return err
	}
	if t.used[KindPrewarm]+w > t.quotas.Prewarm {
		return ErrPrewarmQuota
	}
	return nil
}

// CommitPrewarm 在 CheckPrewarm 与队列检查都通过后计费并推进时钟。
func (m *Meter) CommitPrewarm(now int64, id string, w uint64) {
	t, _ := m.tenant(id, now)
	t.used[KindPrewarm] += w
	m.maxNow = now
}

// Touch 检查时钟与租户，通过则推进时钟（Fill 用）。
func (m *Meter) Touch(now int64, id string) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	if _, err := m.tenant(id, now); err != nil {
		return err
	}
	m.maxNow = now
	return nil
}

// AdvanceClock 检查时钟并推进（Tick 用）。
func (m *Meter) AdvanceClock(now int64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.maxNow = now
	return nil
}
