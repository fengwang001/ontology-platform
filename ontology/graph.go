// Package ontology 提供本体平台上的对象图存储与受权限约束的图遍历环检测能力。
package ontology

import "sync"

// ObjectID 是图中对象的唯一标识。
type ObjectID string

// Label 是附加在链接上的权限标签。
// 调用方只有在自身被授予的标签集合包含该标签时才能看到对应链接。
type Label string

// Link 表示两个对象之间的一条有向链接，附带一个权限标签。
type Link struct {
	ID    string
	From  ObjectID
	To    ObjectID
	Label Label
}

// Store 是可变的对象图存储。
// 它通过互斥锁串行化所有写操作，并支持在某一确定时点取出
// 同时包含图结构与权限标签的一致性快照。
type Store struct {
	mu      sync.RWMutex
	objects map[ObjectID]struct{}
	links   map[string]Link
	out     map[ObjectID][]string // From -> link IDs（保持插入顺序）
	in      map[ObjectID][]string // To   -> link IDs
	version uint64
}

// NewStore 创建一个空的对象图存储。
func NewStore() *Store {
	return &Store{
		objects: make(map[ObjectID]struct{}),
		links:   make(map[string]Link),
		out:     make(map[ObjectID][]string),
		in:      make(map[ObjectID][]string),
	}
}

// AddObject 向图中加入一个对象；重复加入同一对象是幂等的。
func (s *Store) AddObject(id ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[id] = struct{}{}
	s.version++
}

// AddLink 向图中加入一条链接；两端对象不存在时会自动补建。
// 若链接 ID 已存在则返回 ErrLinkExists。
func (s *Store) AddLink(l Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.links[l.ID]; dup {
		return ErrLinkExists
	}
	s.objects[l.From] = struct{}{}
	s.objects[l.To] = struct{}{}
	s.links[l.ID] = l
	s.out[l.From] = append(s.out[l.From], l.ID)
	s.in[l.To] = append(s.in[l.To], l.ID)
	s.version++
	return nil
}

// RemoveLink 删除一条链接；链接不存在时幂等返回。
func (s *Store) RemoveLink(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.links[id]
	if !ok {
		return
	}
	delete(s.links, id)
	s.out[l.From] = removeID(s.out[l.From], id)
	s.in[l.To] = removeID(s.in[l.To], id)
	s.version++
}

// SetLinkLabel 修改一条链接的权限标签；链接不存在时返回 ErrLinkNotFound。
func (s *Store) SetLinkLabel(id string, label Label) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.links[id]
	if !ok {
		return ErrLinkNotFound
	}
	l.Label = label
	s.links[id] = l
	s.version++
	return nil
}

// Version 返回当前存储的版本号，每次成功写操作递增。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

func removeID(ids []string, id string) []string {
	for i, v := range ids {
		if v == id {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}

// Snapshot 是某一确定时点上图结构与权限标签的不可变视图。
// 遍历只在快照上执行，因此并发修改不会影响本次遍历结果。
type Snapshot struct {
	version uint64
	objects map[ObjectID]struct{}
	out     map[ObjectID][]Link
	in      map[ObjectID][]Link
}

// Snapshot 取出当前时点的一致性快照。
// 图结构与权限标签在同一次临界区内拷贝，共享同一个快照时点。
func (s *Store) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := &Snapshot{
		version: s.version,
		objects: make(map[ObjectID]struct{}, len(s.objects)),
		out:     make(map[ObjectID][]Link, len(s.out)),
		in:      make(map[ObjectID][]Link, len(s.in)),
	}
	for id := range s.objects {
		snap.objects[id] = struct{}{}
	}
	for from, ids := range s.out {
		links := make([]Link, 0, len(ids))
		for _, id := range ids {
			links = append(links, s.links[id])
		}
		snap.out[from] = links
	}
	for to, ids := range s.in {
		links := make([]Link, 0, len(ids))
		for _, id := range ids {
			links = append(links, s.links[id])
		}
		snap.in[to] = links
	}
	return snap
}

// Version 返回快照对应的存储版本号。
func (snap *Snapshot) Version() uint64 { return snap.version }
