// Package inlinecache 实现属性访问内联缓存站点管理器。
//
// 全局方法表把形状编号（正整数）映射到目标。站点按名字创建，
// 每个站点缓存最多 M 个（形状，目标）条目，状态由条目数决定：
// 0 为空（Empty），1 为单态（Monomorphic），2..M 为多态（Polymorphic）；
// 一旦缓存溢出即进入超多态（Megamorphic）且永不退出。
package inlinecache

import "sync"

// State 站点缓存状态。
type State int

const (
	StateEmpty       State = iota // 空：0 个条目
	StateMonomorphic              // 单态：1 个条目
	StatePolymorphic              // 多态：2..M 个条目
	StateMegamorphic              // 超多态：缓存溢出，永不退出
)

func (s State) String() string {
	switch s {
	case StateEmpty:
		return "empty"
	case StateMonomorphic:
		return "monomorphic"
	case StatePolymorphic:
		return "polymorphic"
	case StateMegamorphic:
		return "megamorphic"
	}
	return "unknown"
}

// Stats 单个站点的访问统计。Hits+Misses+Megamorphic 恒等于成功访问总次数。
type Stats struct {
	Hits        uint64 // 缓存命中次数
	Misses      uint64 // 缓存未命中（含进入超多态的那次）次数
	Megamorphic uint64 // 超多态状态下的访问次数
}

// Total 返回该站点成功访问的总次数。
func (s Stats) Total() uint64 { return s.Hits + s.Misses + s.Megamorphic }

// Snapshot 站点在某时刻的只读视图。
type Snapshot struct {
	Name    string
	State   State
	Entries map[int]string // 形状 -> 目标
	Stats   Stats
}

type site struct {
	entries     map[int]string
	megamorphic bool
	stats       Stats
}

// Manager 内联缓存站点管理器。所有方法可并发调用，
// 结果等价于某个串行顺序（内部由互斥锁串行化）。
type Manager struct {
	mu       sync.Mutex
	capacity int            // 多态上限 M
	table    map[int]string // 全局方法表：形状 -> 目标
	sites    map[string]*site
}

// NewManager 创建管理器，capacity 为多态上限 M，必须 >= 2。
func NewManager(capacity int) (*Manager, error) {
	if capacity < 2 {
		return nil, ErrInvalidCapacity
	}
	return &Manager{
		capacity: capacity,
		table:    make(map[int]string),
		sites:    make(map[string]*site),
	}, nil
}

// CreateSite 按名字创建站点；名字已存在时整体拒绝。
func (m *Manager) CreateSite(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sites[name]; ok {
		return ErrSiteExists
	}
	m.sites[name] = &site{entries: make(map[int]string)}
	return nil
}

// Define 定义或重定义形状。形状必须为正整数。
// 新定义不影响站点；目标不同的重定义使所有缓存了该形状的
// 非超多态站点删去该条目；目标相同则无影响。
func (m *Manager) Define(shape int, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if shape <= 0 {
		return ErrInvalidShape
	}
	if old, ok := m.table[shape]; ok {
		if old == target {
			return nil
		}
		m.table[shape] = target
		for _, s := range m.sites {
			if s.megamorphic {
				continue
			}
			delete(s.entries, shape)
		}
		return nil
	}
	m.table[shape] = target
	return nil
}

// Access 访问（站点，形状），返回解析到的目标。
// 超多态站点直接查全局方法表并计入超多态访问数；
// 否则命中计一次命中，未命中查表并插入条目计一次未命中，
// 插入前已有 M 个条目则清空缓存进入超多态，该次仍计未命中。
// 站点不存在、形状非法或形状未定义时整体拒绝，不改变任何状态与统计。
func (m *Manager) Access(name string, shape int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sites[name]
	if !ok {
		return "", ErrSiteNotFound
	}
	if shape <= 0 {
		return "", ErrInvalidShape
	}
	target, ok := m.table[shape]
	if !ok {
		return "", ErrShapeUndefined
	}
	if s.megamorphic {
		s.stats.Megamorphic++
		return target, nil
	}
	if cached, ok := s.entries[shape]; ok {
		s.stats.Hits++
		return cached, nil
	}
	s.stats.Misses++
	if len(s.entries) >= m.capacity {
		s.entries = make(map[int]string)
		s.megamorphic = true
		return target, nil
	}
	s.entries[shape] = target
	return target, nil
}

// Inspect 返回站点的只读快照；站点不存在时报错。
func (m *Manager) Inspect(name string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sites[name]
	if !ok {
		return Snapshot{}, ErrSiteNotFound
	}
	entries := make(map[int]string, len(s.entries))
	for k, v := range s.entries {
		entries[k] = v
	}
	return Snapshot{
		Name:    name,
		State:   s.state(),
		Entries: entries,
		Stats:   s.stats,
	}, nil
}

// Lookup 查询全局方法表中形状对应的目标。
func (m *Manager) Lookup(shape int) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.table[shape]
	return t, ok
}

func (s *site) state() State {
	if s.megamorphic {
		return StateMegamorphic
	}
	switch len(s.entries) {
	case 0:
		return StateEmpty
	case 1:
		return StateMonomorphic
	default:
		return StatePolymorphic
	}
}
