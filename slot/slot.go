// Package slot 单层轮里一个槽的元素集合：插入、按句柄删除、取走整槽。
package slot

import "sort"

// Entry 槽内元素。H 为句柄（可比较），Seq 为注册序号，Deadline 为绝对到期 tick。
type Entry struct {
	H        any
	Seq      uint64
	Deadline int64
}

// Slot 一个槽的元素集合。
type Slot struct {
	m map[any]Entry
}

// New 创建空槽。
func New() *Slot { return &Slot{m: make(map[any]Entry)} }

// Insert 插入元素；同句柄重复插入会覆盖。
func (s *Slot) Insert(e Entry) { s.m[e.H] = e }

// Remove 按句柄删除，返回句柄是否存在。
func (s *Slot) Remove(h any) bool {
	if _, ok := s.m[h]; !ok {
		return false
	}
	delete(s.m, h)
	return true
}

// Entries 返回按 Seq 升序的全部元素，不清空槽。
func (s *Slot) Entries() []Entry {
	out := make([]Entry, 0, len(s.m))
	for _, e := range s.m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// TakeAll 取走整槽：返回按 Seq 升序的全部元素并清空槽。
func (s *Slot) TakeAll() []Entry {
	out := s.Entries()
	clear(s.m)
	return out
}

// Len 返回元素个数。
func (s *Slot) Len() int { return len(s.m) }
