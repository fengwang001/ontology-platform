// Package rel 保存函数关系 R(a→b) 与一对多关系 S(b→{c})，只做基本读写。
package rel

import (
	"errors"
	"sort"
)

// 哨兵错误：S 侧的两类可判定失败。
var (
	ErrDuplicatePair = errors.New("rel: (b,c) already exists")
	ErrMissingPair   = errors.New("rel: (b,c) does not exist")
)

// Store 是 R 与 S 的纯内存存储，本身不加锁，由上层 jidx 统一加锁。
type Store struct {
	r map[int]int
	s map[int]map[int]struct{}
}

// New 创建空存储。
func New() *Store {
	return &Store{r: map[int]int{}, s: map[int]map[int]struct{}{}}
}

// SetR 令 R[a]=b，返回旧值 old 与 a 原先是否存在。
func (s *Store) SetR(a, b int) (old int, existed bool) {
	old, existed = s.r[a]
	s.r[a] = b
	return old, existed
}

// DelR 删除 a，返回其旧值与是否存在。
func (s *Store) DelR(a int) (old int, existed bool) {
	old, existed = s.r[a]
	if existed {
		delete(s.r, a)
	}
	return old, existed
}

// GetR 返回 R[a]。
func (s *Store) GetR(a int) (b int, ok bool) {
	b, ok = s.r[a]
	return b, ok
}

// AddS 把 c 加入 S[b]；重复返回 ErrDuplicatePair 且不改状态。
func (s *Store) AddS(b, c int) error {
	set := s.s[b]
	if set == nil {
		set = map[int]struct{}{}
		s.s[b] = set
	}
	if _, dup := set[c]; dup {
		return ErrDuplicatePair
	}
	set[c] = struct{}{}
	return nil
}

// DelS 把 c 从 S[b] 移除；不存在返回 ErrMissingPair 且不改状态。
func (s *Store) DelS(b, c int) error {
	set := s.s[b]
	if _, ok := set[c]; !ok {
		return ErrMissingPair
	}
	delete(set, c)
	return nil
}

// HasS 判定 (b,c) 是否存在。
func (s *Store) HasS(b, c int) bool {
	_, ok := s.s[b][c]
	return ok
}

// Members 返回 S[b] 的升序切片；不存在返回 nil。
func (s *Store) Members(b int) []int {
	set := s.s[b]
	if len(set) == 0 {
		return nil
	}
	out := make([]int, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Ints(out)
	return out
}

// RangeR 以任意顺序对每条 R 条目调用 fn；fn 返回 false 时停止。
func (s *Store) RangeR(fn func(a, b int) bool) {
	for a, b := range s.r {
		if !fn(a, b) {
			return
		}
	}
}

// RLen 返回 R 条目数。
func (s *Store) RLen() int { return len(s.r) }
