package lease

import (
	"errors"
	"sort"
)

var (
	ErrInvalidArgument = errors.New("lease: invalid argument")
	ErrClockBacktrack  = errors.New("lease: clock backtrack")
	ErrLeaseExists     = errors.New("lease: lease already exists")
	ErrLeaseNotFound   = errors.New("lease: lease not found")
	ErrLeaseBacktrack  = errors.New("lease: lease r backtrack")
	ErrHistoryMissing  = errors.New("lease: history not available below H")
	ErrLeaseLimit      = errors.New("lease: lease count exceeds Lmax")
	ErrGCPBacktrack    = errors.New("lease: global checkpoint backtrack")
)

// View 是 Manager 判定 r 合法性所需的主历史视图（窄接口，避免包环依赖）。
type View interface {
	H() int
	MaxSeq() int
}

type entry struct {
	r         int
	lastRenew int
}

// Manager 管理保留租约与全局检查点。所有方法假定调用方已做参数与时钟校验，
// 并在同一把锁内调用；剔除只发生在 MergeFloor 中。
type Manager struct {
	e      int
	lmax   int
	view   View
	gcp    int
	leases map[string]*entry
}

func New(validFor, lmax int, view View) *Manager {
	return &Manager{
		e:      validFor,
		lmax:   lmax,
		view:   view,
		leases: make(map[string]*entry),
	}
}

func (m *Manager) GCP() int { return m.gcp }

func (m *Manager) Count() int { return len(m.leases) }

func (m *Manager) Has(name string) bool {
	_, ok := m.leases[name]
	return ok
}

// LeaseView 是租约的只读快照（按名字升序），供测试与可观测性使用。
type LeaseView struct {
	Name      string
	R         int
	LastRenew int
}

// Snapshot 返回现存（尚未剔除）租约，按名字升序；过期但未剔除的租约仍包含在内。
func (m *Manager) Snapshot() []LeaseView {
	out := make([]LeaseView, 0, len(m.leases))
	for name, e := range m.leases {
		out = append(out, LeaseView{Name: name, R: e.r, LastRenew: e.lastRenew})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AddLease 假定 1 ≤ r ≤ maxSeq+1、name 非空；按存在性 > 历史不可得 > 超限拒绝。
func (m *Manager) AddLease(now int, name string, r int) error {
	if m.Has(name) {
		return ErrLeaseExists
	}
	if r < m.view.H() {
		return ErrHistoryMissing
	}
	if len(m.leases) >= m.lmax {
		return ErrLeaseLimit
	}
	m.leases[name] = &entry{r: r, lastRenew: now}
	return nil
}

// RenewLease 假定 name 非空；r 小于原值报租约回退。过期但尚未剔除仍可续活。
func (m *Manager) RenewLease(now int, name string, r int) error {
	e, ok := m.leases[name]
	if !ok {
		return ErrLeaseNotFound
	}
	if r < e.r {
		return ErrLeaseBacktrack
	}
	e.r = r
	e.lastRenew = now
	return nil
}

func (m *Manager) RemoveLease(name string) error {
	if !m.Has(name) {
		return ErrLeaseNotFound
	}
	delete(m.leases, name)
	return nil
}

// SetGCP 假定 g ≤ maxSeq；g 小于当前 gcp 报检查点回退。
func (m *Manager) SetGCP(g int) error {
	if g < m.gcp {
		return ErrGCPBacktrack
	}
	m.gcp = g
	return nil
}

// MergeFloor 先剔除全部过期租约，再返回 floor=min(gcp+1, 现存租约最小 r)
// 与被剔除租约名（升序）。仅此处剔除租约。
func (m *Manager) MergeFloor(now int) (int, []string) {
	removed := m.expire(now)
	floor := m.gcp + 1
	for _, e := range m.leases {
		if e.r < floor {
			floor = e.r
		}
	}
	return floor, removed
}

func (m *Manager) expire(now int) []string {
	var removed []string
	for name, e := range m.leases {
		if now-e.lastRenew > m.e {
			removed = append(removed, name)
			delete(m.leases, name)
		}
	}
	sort.Strings(removed)
	return removed
}
