// Package sch 纯数据结构：父行集合、子行集合、每个父行的引用计数。
// 不依赖其他包。
package sch

// State 持有全部内存状态。delScan 记录最近一次 DelParent 为判定
// 「是否存在引用子行」扫描过的子行个数——实现走引用计数，故恒为 0。
// 它是非导出字段，不出现在任何公开接口里。
type State struct {
	parents  map[string]struct{}
	children map[string]string // ck -> parent pk
	refs     map[string]int    // pk -> 引用它的子行数
	delScan  int
}

func New() *State {
	return &State{
		parents:  make(map[string]struct{}),
		children: make(map[string]string),
		refs:     make(map[string]int),
	}
}

func (s *State) HasParent(pk string) bool {
	_, ok := s.parents[pk]
	return ok
}

// AddParent 插入父行。调用方须保证其不存在。
func (s *State) AddParent(pk string) {
	s.parents[pk] = struct{}{}
}

// RefCount 返回父行 pk 当前被引用的子行数。
func (s *State) RefCount(pk string) int { return s.refs[pk] }

// ParentDeletable 判定父行是否存在、是否仍被引用。
// 「被引用」走引用计数，不扫描子表，故 delScan 恒置 0。
func (s *State) ParentDeletable(pk string) (exists, inUse bool) {
	s.delScan = 0
	return s.HasParent(pk), s.refs[pk] > 0
}

// DelParent 删除父行及其计数项。调用方须保证其存在且无引用。
func (s *State) DelParent(pk string) {
	delete(s.parents, pk)
	delete(s.refs, pk)
}

func (s *State) HasChild(ck string) bool {
	_, ok := s.children[ck]
	return ok
}

// AddChild 插入子行并给父行计数 +1。调用方须保证 ck 不存在、pk 存在。
func (s *State) AddChild(ck, pk string) {
	s.children[ck] = pk
	s.refs[pk]++
}

// DelChild 删除子行并给父行计数 -1。调用方须保证 ck 存在。
func (s *State) DelChild(ck string) {
	pk := s.children[ck]
	delete(s.children, ck)
	s.refs[pk]--
}

// ParentSet 返回父行集合的副本。
func (s *State) ParentSet() map[string]bool {
	out := make(map[string]bool, len(s.parents))
	for pk := range s.parents {
		out[pk] = true
	}
	return out
}

// ChildMap 返回子表（ck->pk）的副本。
func (s *State) ChildMap() map[string]string {
	out := make(map[string]string, len(s.children))
	for ck, pk := range s.children {
		out[ck] = pk
	}
	return out
}

// RefMap 返回引用计数的副本（供自检核对）。
func (s *State) RefMap() map[string]int {
	out := make(map[string]int, len(s.refs))
	for pk, n := range s.refs {
		out[pk] = n
	}
	return out
}
