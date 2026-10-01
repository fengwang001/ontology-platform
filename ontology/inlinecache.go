// Package ontology 实现属性访问的内联缓存（Inline Cache）站点管理器。
package ontology

import (
	"errors"
	"reflect"
	"sort"
	"sync"
)

// State 表示一个缓存站点的状态。
type State int

const (
	// StateEmpty 空态：缓存条目数为 0。
	StateEmpty State = iota
	// StateMonomorphic 单态：缓存条目数为 1。
	StateMonomorphic
	// StatePolymorphic 多态：缓存条目数在 2..M 之间。
	StatePolymorphic
	// StateMegamorphic 超多态：一旦进入永不退出。
	StateMegamorphic
)

// String 返回状态名。
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
	default:
		return "unknown"
	}
}

// ErrorCode 用于区分不同的拒绝原因。
type ErrorCode string

const (
	// ErrInvalidLimit 构造时多态上限 M 小于 2。
	ErrInvalidLimit ErrorCode = "invalid_limit"
	// ErrInvalidShape 形状编号不是正整数。
	ErrInvalidShape ErrorCode = "invalid_shape"
	// ErrSiteExists 创建站点时名字已存在。
	ErrSiteExists ErrorCode = "site_exists"
	// ErrSiteNotFound 访问时站点不存在。
	ErrSiteNotFound ErrorCode = "site_not_found"
	// ErrShapeUndefined 形状未在全局方法表中定义。
	ErrShapeUndefined ErrorCode = "shape_undefined"
)

// OpError 携带可区分错误码的拒绝错误。
type OpError struct {
	Code   ErrorCode
	Op     string
	Site   string
	Shape  int
	Reason string
}

func (e *OpError) Error() string {
	site := ""
	if e.Site != "" {
		site = " site=" + e.Site
	}
	return "ontology: " + e.Op + ":" + site + " " + string(e.Code) + ": " + e.Reason
}

// ErrorCodeOf 从错误中提取错误码；非 OpError 返回空串。
func ErrorCodeOf(err error) ErrorCode {
	var opErr *OpError
	if errors.As(err, &opErr) {
		return opErr.Code
	}
	return ""
}

// Stats 是单个站点的访问统计。
type Stats struct {
	Hits        int64
	Misses      int64
	Megamorphic int64
}

// Total 返回该站点成功访问的总次数。
func (s Stats) Total() int64 { return s.Hits + s.Misses + s.Megamorphic }

// Entry 是一条（形状，目标）缓存条目。
type Entry struct {
	Shape  int
	Target any
}

// SiteSnapshot 是站点某一时刻的可观测快照。
type SiteSnapshot struct {
	Name    string
	State   State
	Entries []Entry
	Stats   Stats
}

// Manager 是属性访问内联缓存站点管理器。
type Manager struct {
	mu    sync.RWMutex
	limit int
	table map[int]any
	sites map[string]*siteState
}

type siteState struct {
	entries []Entry
	mega    bool
	hits    int64
	misses  int64
	megaOps int64
}

// NewManager 创建管理器；M 必须 >= 2，否则返回 ErrInvalidLimit。
func NewManager(m int) (*Manager, error) {
	if m < 2 {
		return nil, &OpError{Code: ErrInvalidLimit, Op: "new", Reason: "polymorphic limit M must be >= 2"}
	}
	return &Manager{
		limit: m,
		table: make(map[int]any),
		sites: make(map[string]*siteState),
	}, nil
}

// CreateSite 按名字创建站点；名字已存在时整体拒绝（ErrSiteExists）。
func (m *Manager) CreateSite(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sites[name]; ok {
		return &OpError{Code: ErrSiteExists, Op: "create_site", Site: name, Reason: "site already exists"}
	}
	m.sites[name] = &siteState{}
	return nil
}

// Define 在全局方法表中定义或重定义形状 -> 目标。
// 形状必须是正整数。形状已定义且目标不同（重定义）时，
// 所有缓存了该形状的非超多态站点删去该条目；目标相同则无影响。
func (m *Manager) Define(shape int, target any) error {
	if shape <= 0 {
		return &OpError{Code: ErrInvalidShape, Op: "define", Shape: shape, Reason: "shape must be a positive integer"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, defined := m.table[shape]
	if defined && sameTarget(old, target) {
		return nil
	}
	m.table[shape] = target
	if !defined {
		return nil
	}
	// 重定义：所有缓存了该形状的非超多态站点删去该条目。
	for _, st := range m.sites {
		if st.mega {
			continue
		}
		for i, e := range st.entries {
			if e.Shape == shape {
				st.entries = append(st.entries[:i], st.entries[i+1:]...)
				break
			}
		}
	}
	return nil
}

// Access 执行一次（站点，形状）属性访问。
func (m *Manager) Access(name string, shape int) (any, error) {
	if shape <= 0 {
		return nil, &OpError{Code: ErrInvalidShape, Op: "access", Site: name, Shape: shape, Reason: "shape must be a positive integer"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sites[name]
	if !ok {
		return nil, &OpError{Code: ErrSiteNotFound, Op: "access", Site: name, Reason: "site does not exist"}
	}
	target, defined := m.table[shape]
	if !defined {
		return nil, &OpError{Code: ErrShapeUndefined, Op: "access", Site: name, Shape: shape, Reason: "shape is not defined in the method table"}
	}
	// 校验全部通过后才允许改变站点与计数。
	if st.mega {
		st.megaOps++
		return target, nil
	}
	for _, e := range st.entries {
		if e.Shape == shape {
			st.hits++
			return e.Target, nil
		}
	}
	st.misses++
	if len(st.entries) >= m.limit {
		// 第 M+1 个不同形状：清空缓存并进入超多态，本次仍计未命中。
		st.entries = nil
		st.mega = true
	} else {
		st.entries = append(st.entries, Entry{Shape: shape, Target: target})
	}
	return target, nil
}

// Snapshot 返回指定站点的状态快照；站点不存在返回 ErrSiteNotFound。
func (m *Manager) Snapshot(name string) (SiteSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.sites[name]
	if !ok {
		return SiteSnapshot{}, &OpError{Code: ErrSiteNotFound, Op: "snapshot", Site: name, Reason: "site does not exist"}
	}
	return st.snapshot(name, m.limit), nil
}

// Stats 返回指定站点的统计；站点不存在返回 ErrSiteNotFound。
func (m *Manager) Stats(name string) (Stats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.sites[name]
	if !ok {
		return Stats{}, &OpError{Code: ErrSiteNotFound, Op: "stats", Site: name, Reason: "site does not exist"}
	}
	return Stats{Hits: st.hits, Misses: st.misses, Megamorphic: st.megaOps}, nil
}

// Sites 返回所有站点名字（确定性字典序）。
func (m *Manager) Sites() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.sites))
	for name := range m.sites {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TableSnapshot 返回全局方法表的副本。
func (m *Manager) TableSnapshot() map[int]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[int]any, len(m.table))
	for shape, target := range m.table {
		out[shape] = target
	}
	return out
}

func (st *siteState) state(limit int) State {
	switch {
	case st.mega:
		return StateMegamorphic
	case len(st.entries) == 0:
		return StateEmpty
	case len(st.entries) == 1:
		return StateMonomorphic
	default:
		return StatePolymorphic
	}
}

func (st *siteState) snapshot(name string, limit int) SiteSnapshot {
	var entries []Entry
	if len(st.entries) > 0 {
		entries = make([]Entry, len(st.entries))
		copy(entries, st.entries)
	}
	return SiteSnapshot{
		Name:    name,
		State:   st.state(limit),
		Entries: entries,
		Stats:   Stats{Hits: st.hits, Misses: st.misses, Megamorphic: st.megaOps},
	}
}

// sameTarget 比较两个目标是否相等；对不可比较类型回退到 reflect.DeepEqual。
func sameTarget(a, b any) bool {
	if reflect.TypeOf(a) == nil || reflect.TypeOf(b) == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(a, b)
}
