// Package mset 维护单个组的多重集 Val→mult，distinct 数 O(1) 增量维护，
// mult 归 0 的条目立即删除。不依赖其他包。
package mset

import "errors"

// ErrZero 撤回一个 mult==0 的 Val。
var ErrZero = errors.New("mset: withdraw of a value whose multiplicity is zero")

// Set 是单个 Group 的多重集。零值不可用，须用 New。
type Set struct {
	m map[string]int // Val → mult；mult==0 的条目不得存在
	d int            // m 中条目数，即 distinct 数
}

// New 创建空多重集。
func New() *Set {
	return &Set{m: make(map[string]int)}
}

// Add 插入一次 v，返回 distinct 数是否发生 0→1 跨越。
func (s *Set) Add(v string) bool {
	if s.m[v] == 0 {
		s.m[v] = 1
		s.d++
		return true
	}
	s.m[v]++
	return false
}

// Remove 撤回一次 v；若当前 mult==0 返回 ErrZero。
// 否则返回 distinct 数是否发生 1→0 跨越（归 0 时删除条目）。
func (s *Set) Remove(v string) (bool, error) {
	c := s.m[v]
	if c == 0 {
		return false, ErrZero
	}
	if c == 1 {
		delete(s.m, v) // mult 归 0 立即删除，不再占内存
		s.d--
		return true, nil
	}
	s.m[v] = c - 1
	return false, nil
}

// Distinct 返回 mult>0 的 Val 个数，O(1)。
func (s *Set) Distinct() int { return s.d }

// Entries 返回当前所有 (Val → mult) 的副本，供不变量核验。
func (s *Set) Entries() map[string]int {
	cp := make(map[string]int, len(s.m))
	for v, n := range s.m {
		cp[v] = n
	}
	return cp
}
