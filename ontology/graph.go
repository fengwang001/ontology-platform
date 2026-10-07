package ontology

import (
	"errors"
	"sort"
	"sync"
)

// Link 是完整图中的一条有向链接，附带一个权限标签。
// 只有被授予该标签的调用方才能看到这条链接；看不到该链接的调用方
// 对其存在与其连接的对象均不可感知。
type Link struct {
	ID    string
	From  string
	To    string
	Label string
}

// Snapshot 是某个确定时点上的不可变图快照。
// 图结构（对象、链接）与权限标签（Link.Label）位于同一不可变对象中，
// 因此结构与标签必然来自同一个时点，杜绝两类修改各取一个时点。
type Snapshot struct {
	version  int64
	objects  map[string]struct{}
	out      map[string][]*Link // from -> 按 ID 排序的出链接
	incident map[string][]*Link // 对象 -> 与之关联的链接（可见性判定用）
}

// Version 返回快照版本号，单调递增。
func (s *Snapshot) Version() int64 { return s.version }

// GraphStore 支持并发读写。读返回不可变快照；写采用整份状态替换
// （copy-on-write），进行中的遍历继续持有旧快照，天然快照隔离。
type GraphStore struct {
	mu     sync.RWMutex
	states map[int64]*graphState // 版本号 -> 不可变状态（供快照回放核对）
	state  *graphState
}

type graphState struct {
	version int64
	objects map[string]struct{}
	links   map[string]*Link
}

// NewGraphStore 创建空图存储。
func NewGraphStore() *GraphStore {
	initial := &graphState{
		version: 0,
		objects: map[string]struct{}{},
		links:   map[string]*Link{},
	}
	return &GraphStore{
		states: map[int64]*graphState{0: initial},
		state:  initial,
	}
}

// Snapshot 获取当前确定时点的不可变快照。
func (g *GraphStore) Snapshot() *Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.state.snapshot()
}

// SnapshotAt 回放指定版本的不可变快照，供测试核对遍历所绑定的时点。
func (g *GraphStore) SnapshotAt(version int64) (*Snapshot, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	st, ok := g.states[version]
	if !ok {
		return nil, false
	}
	return st.snapshot(), true
}

func (s *graphState) snapshot() *Snapshot {
	snap := &Snapshot{
		version:  s.version,
		objects:  s.objects,
		out:      map[string][]*Link{},
		incident: map[string][]*Link{},
	}
	for _, lk := range s.links {
		snap.out[lk.From] = append(snap.out[lk.From], lk)
		snap.incident[lk.From] = append(snap.incident[lk.From], lk)
		snap.incident[lk.To] = append(snap.incident[lk.To], lk)
	}
	for from := range snap.out {
		sort.Slice(snap.out[from], func(i, j int) bool {
			return snap.out[from][i].ID < snap.out[from][j].ID
		})
	}
	return snap
}

func (g *GraphStore) mutate(fn func(*graphState) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	next := &graphState{
		version: g.state.version + 1,
		objects: make(map[string]struct{}, len(g.state.objects)),
		links:   make(map[string]*Link, len(g.state.links)),
	}
	for o := range g.state.objects {
		next.objects[o] = struct{}{}
	}
	for id, lk := range g.state.links {
		cp := *lk
		next.links[id] = &cp
	}
	if err := fn(next); err != nil {
		return err
	}
	g.state = next
	g.states[next.version] = next
	return nil
}

// AddObject 向图中加入对象。
func (g *GraphStore) AddObject(id string) {
	_ = g.mutate(func(st *graphState) error {
		st.objects[id] = struct{}{}
		return nil
	})
}

// ErrLinkExists 链接 ID 已存在。
var ErrLinkExists = errors.New("link id already exists")

// AddLink 加入链接（结构与权限标签同一版本原子生效）。
func (g *GraphStore) AddLink(link Link) error {
	if link.ID == "" {
		return errors.New("link id is required")
	}
	return g.mutate(func(st *graphState) error {
		if _, ok := st.links[link.ID]; ok {
			return ErrLinkExists
		}
		if _, ok := st.objects[link.From]; !ok {
			return errors.New("source object not found: " + link.From)
		}
		if _, ok := st.objects[link.To]; !ok {
			return errors.New("target object not found: " + link.To)
		}
		cp := link
		st.links[link.ID] = &cp
		return nil
	})
}

// RemoveLink 删除链接。
func (g *GraphStore) RemoveLink(id string) {
	_ = g.mutate(func(st *graphState) error {
		delete(st.links, id)
		return nil
	})
}

// ErrLinkNotFound 链接不存在。
var ErrLinkNotFound = errors.New("link not found")

// SetLinkLabel 修改链接权限标签（与图状态同版本原子生效）。
func (g *GraphStore) SetLinkLabel(id, label string) error {
	return g.mutate(func(st *graphState) error {
		lk, ok := st.links[id]
		if !ok {
			return ErrLinkNotFound
		}
		cp := *lk
		cp.Label = label
		st.links[id] = &cp
		return nil
	})
}
