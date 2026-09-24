// Package rel 提供二元元组 (x,y) 的集合表示与集合操作。
// 不依赖本工程其他任何包。
package rel

import "sort"

// T 是一个二元元组 (X, Y)。
type T struct {
	X, Y string
}

// Set 是元组的集合（集合语义，重复加入只保留一份）。
type Set struct {
	m map[T]struct{}
}

// New 创建集合并可放入若干初始元组。
func New(ts ...T) *Set {
	s := &Set{m: make(map[T]struct{}, len(ts))}
	for _, t := range ts {
		s.Add(t)
	}
	return s
}

// Add 把 t 加入集合；返回 true 表示它是新元组，false 表示原先已存在。
func (s *Set) Add(t T) bool {
	if _, ok := s.m[t]; ok {
		return false
	}
	s.m[t] = struct{}{}
	return true
}

// Has 报告 t 是否在集合中。
func (s *Set) Has(t T) bool {
	_, ok := s.m[t]
	return ok
}

// Each 以不保证顺序的方式遍历集合中的每个元组。
func (s *Set) Each(fn func(T)) {
	for t := range s.m {
		fn(t)
	}
}

// Size 返回集合中元组个数。
func (s *Set) Size() int { return len(s.m) }

// Diff 返回一个新集合：在 s 中但不在 other 中的元组（s − other）。
func (s *Set) Diff(other *Set) *Set {
	out := New()
	for t := range s.m {
		if !other.Has(t) {
			out.Add(t)
		}
	}
	return out
}

// Union 把 other 的全部元组并入 s（原地）。
func (s *Set) Union(other *Set) {
	for t := range other.m {
		s.Add(t)
	}
}

// Clone 返回 s 的独立副本。
func (s *Set) Clone() *Set {
	out := New()
	out.Union(s)
	return out
}

// Sorted 按 (X, Y) 字典序返回全部元组，保证遍历确定性。
func (s *Set) Sorted() []T {
	out := make([]T, 0, len(s.m))
	for t := range s.m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		return out[i].Y < out[j].Y
	})
	return out
}
