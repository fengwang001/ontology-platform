package graph

import "fmt"

// AddObject 向图中加入一个对象；ID 重复时返回错误。
func (s *Store) AddObject(obj Object) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[obj.ID]; ok {
		return fmt.Errorf("object %q already exists", obj.ID)
	}
	s.objects[obj.ID] = obj
	s.version++
	return nil
}

// RemoveObject 删除对象及其关联的所有链接；对象不存在时返回错误。
func (s *Store) RemoveObject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[id]; !ok {
		return fmt.Errorf("object %q not found", id)
	}
	delete(s.objects, id)
	for _, l := range s.out[id] {
		s.removeLinkLocked(l.ID)
	}
	for _, l := range s.in[id] {
		s.removeLinkLocked(l.ID)
	}
	s.version++
	return nil
}

// AddLink 加入一条链接；两端对象必须已存在，链接 ID 不得重复。
func (s *Store) AddLink(l Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[l.ID]; ok {
		return fmt.Errorf("link %q already exists", l.ID)
	}
	if _, ok := s.objects[l.SourceID]; !ok {
		return fmt.Errorf("source object %q not found", l.SourceID)
	}
	if _, ok := s.objects[l.TargetID]; !ok {
		return fmt.Errorf("target object %q not found", l.TargetID)
	}
	s.links[l.ID] = l
	s.out[l.SourceID] = append(s.out[l.SourceID], l)
	s.in[l.TargetID] = append(s.in[l.TargetID], l)
	s.version++
	return nil
}

// RemoveLink 删除一条链接；链接不存在时返回错误。
func (s *Store) RemoveLink(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.links[id]; !ok {
		return fmt.Errorf("link %q not found", id)
	}
	s.removeLinkLocked(id)
	s.version++
	return nil
}

func (s *Store) removeLinkLocked(id string) {
	l := s.links[id]
	delete(s.links, id)
	s.out[l.SourceID] = removeLink(s.out[l.SourceID], id)
	s.in[l.TargetID] = removeLink(s.in[l.TargetID], id)
}

func removeLink(links []Link, id string) []Link {
	for i, l := range links {
		if l.ID == id {
			return append(links[:i], links[i+1:]...)
		}
	}
	return links
}

// Version 返回当前图结构的版本号，每次修改单调递增。
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Snapshot 在读锁内深拷贝图结构，返回一个与后续并发修改完全隔离的视图。
// 在快照上执行的遍历，其结果等价于在快照版本对应的确定图结构上执行的结果。
func (s *Store) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := &Snapshot{
		objects: make(map[string]Object, len(s.objects)),
		out:     make(map[string][]Link, len(s.out)),
		in:      make(map[string][]Link, len(s.in)),
		version: s.version,
	}
	for id, o := range s.objects {
		snap.objects[id] = o
	}
	for id, ls := range s.out {
		snap.out[id] = append([]Link(nil), ls...)
	}
	for id, ls := range s.in {
		snap.in[id] = append([]Link(nil), ls...)
	}
	return snap
}

// Version 返回快照对应的图结构版本号。
func (s *Snapshot) Version() uint64 { return s.version }

// HasObject 报告快照中是否存在给定对象。
func (s *Snapshot) HasObject(id string) bool {
	_, ok := s.objects[id]
	return ok
}

// Links 返回快照中从 id 出发、沿给定方向的链接（未排序，调用方负责排序）。
func (s *Snapshot) Links(id string, dir Direction) []Link {
	if dir == Outgoing {
		return s.out[id]
	}
	return s.in[id]
}
