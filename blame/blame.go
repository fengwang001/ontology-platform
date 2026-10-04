// Package blame 在数据集依赖图上做新鲜度违约归因、告警聚合与去重。
// Monitor 是对外入口：AddDataset/Land/Evaluate/Blame 共用单调时钟，
// 所有方法可并发调用，效果等价于某个串行顺序。
package blame

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/dag"
	"ontology/fresh"
)

// MaxNow 是 now 的上界（秒）。
const MaxNow = int64(1_000_000_000_000)

var (
	ErrInvalid         = dag.ErrInvalid
	ErrFrozen          = dag.ErrFrozen
	ErrExists          = dag.ErrExists
	ErrNoSuchParent    = dag.ErrNoSuchParent
	ErrTooMany         = dag.ErrTooMany
	ErrNoSuchDataset   = dag.ErrNoSuchDataset
	ErrAlready         = fresh.ErrAlready
	ErrOutOfOrder      = fresh.ErrOutOfOrder
	ErrTooEarly        = fresh.ErrTooEarly
	ErrUpstreamMissing = fresh.ErrUpstreamMissing
	ErrClock           = errors.New("blame: clock moved backwards")
	ErrNotFound        = errors.New("blame: no frozen blame for dataset period")
)

// Kind 是根因类别。
type Kind int

const (
	// Self 表示根因是数据集自身（无父，或上游给足了时间）。
	Self Kind = iota
	// Unreachable 表示关键父不违约，即上游即使按时也不够本数据集用。
	Unreachable
)

func (k Kind) String() string {
	if k == Unreachable {
		return "Unreachable"
	}
	return "Self"
}

// Alert 是一次评估中按 (root, kind, k) 聚合出的告警。
type Alert struct {
	Root     string
	Kind     Kind
	K        int64
	Affected []string
}

// Decision 是冻结的归因结果。
type Decision struct {
	Root string
	Kind Kind
}

type period struct {
	d string
	k int64
}

// Monitor 是新鲜度监控器。
type Monitor struct {
	mu       sync.Mutex
	g        *dag.Graph
	f        *fresh.Store
	clock    int64
	started  bool
	blamed   map[period]Decision
	examined int64 // Evaluate 为判断违约而考察的 (d,k) 对数
}

// New 创建周期长度为 T 的监控器。
func New(T int64) (*Monitor, error) {
	g, err := dag.New(T)
	if err != nil {
		return nil, err
	}
	return &Monitor{g: g, f: fresh.New(g), blamed: make(map[period]Decision)}, nil
}

// AddDataset 登记数据集；首次被接受的 Land/Evaluate 之后报 ErrFrozen。
func (m *Monitor) AddDataset(name string, off, dur int64, parents []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.g.AddDataset(name, off, dur, parents)
}

// Land 记录 d 第 k 期在 now 落地。
// 拒绝次序：参数非法 > 时钟回退 > 数据集不存在 > ErrAlready/ErrOutOfOrder
// > ErrTooEarly > ErrUpstreamMissing。被拒绝时不改任何状态（含时钟）。
func (m *Monitor) Land(d string, k, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k < 0 || now < 0 || now > MaxNow {
		return fmt.Errorf("%w: k=%d now=%d", ErrInvalid, k, now)
	}
	if m.started && now < m.clock {
		return fmt.Errorf("%w: now=%d < clock=%d", ErrClock, now, m.clock)
	}
	if _, ok := m.g.Get(d); !ok {
		return fmt.Errorf("%w: %s", ErrNoSuchDataset, d)
	}
	if err := m.f.Land(d, k, now); err != nil {
		return err
	}
	m.accept(now)
	return nil
}

// Evaluate 找出在 now 违约且此前未告警过的 (d,k)，逐个归因并聚合告警。
// 返回按 (k, root 名字节序, kind 先 Self 后 Unreachable) 升序的告警列表。
func (m *Monitor) Evaluate(now int64) ([]Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < 0 || now > MaxNow {
		return nil, fmt.Errorf("%w: now=%d", ErrInvalid, now)
	}
	if m.started && now < m.clock {
		return nil, fmt.Errorf("%w: now=%d < clock=%d", ErrClock, now, m.clock)
	}
	m.accept(now)
	type key struct {
		root string
		kind Kind
		k    int64
	}
	groups := make(map[key][]string)
	for _, name := range m.g.Names() {
		k := m.f.FirstOpen(name)
		for {
			m.examined++
			if !m.f.Violation(name, k, now) {
				break // 未结案期单调：首个不违约之后都不会违约
			}
			root, kind := m.attribute(name, k, now)
			gk := key{root, kind, k}
			groups[gk] = append(groups[gk], name)
			m.blamed[period{name, k}] = Decision{Root: root, Kind: kind}
			m.f.Close(name, k)
			k = m.f.NextOpen(name, k)
		}
	}
	alerts := make([]Alert, 0, len(groups))
	for gk, affected := range groups {
		slices.Sort(affected)
		alerts = append(alerts, Alert{Root: gk.root, Kind: gk.kind, K: gk.k, Affected: affected})
	}
	slices.SortFunc(alerts, func(a, b Alert) int {
		if c := cmp.Compare(a.K, b.K); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Root, b.Root); c != 0 {
			return c
		}
		return cmp.Compare(a.Kind, b.Kind)
	})
	return alerts, nil
}

// Blame 返回 (d,k) 已冻结的根因；未告警过则报 ErrNotFound。只读。
func (m *Monitor) Blame(d string, k int64) (Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dec, ok := m.blamed[period{d, k}]
	if !ok {
		return Decision{}, fmt.Errorf("%w: %s period %d", ErrNotFound, d, k)
	}
	return dec, nil
}

// accept 推进时钟并冻结注册表（仅在被接受的操作上调用）。
func (m *Monitor) accept(now int64) {
	m.clock = now
	m.started = true
	m.g.Freeze()
}

// attribute 在时刻 t 对违约的 (d,k) 沿关键父单链追溯根因。
func (m *Monitor) attribute(d string, k, t int64) (string, Kind) {
	x := d
	for {
		ds, _ := m.g.Get(x)
		if len(ds.Parents) == 0 {
			return x, Self
		}
		hasUnlanded := false
		minUnlanded := ""
		latest := int64(0)
		latestName := ""
		for _, p := range ds.Parents {
			lt, ok := m.f.Landed(p, k)
			if !ok {
				hasUnlanded = true
				if minUnlanded == "" || p < minUnlanded {
					minUnlanded = p
				}
				continue
			}
			if latestName == "" || lt > latest || (lt == latest && p < latestName) {
				latest, latestName = lt, p
			}
		}
		if !hasUnlanded && latest+ds.Dur <= m.f.Deadline(x, k) {
			return x, Self // 上游给足了时间
		}
		crit := latestName
		if hasUnlanded {
			crit = minUnlanded
		}
		if !m.f.Violation(crit, k, t) {
			return x, Unreachable // 上游即使按时也不够 x 用
		}
		x = crit
	}
}
