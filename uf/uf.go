package uf

import (
	"errors"
	"sync"
)

var (
	ErrBadIndex     = errors.New("uf: index out of range")
	ErrNegativeSize = errors.New("uf: size cannot be negative")
	ErrNilSet       = errors.New("uf: nil union-find set")
)

type Set struct {
	parent, rank []int
	count, hops  int
	mu           sync.Mutex
}

func New(n int) *Set {
	if n < 0 {
		panic(ErrNegativeSize)
	}
	s := &Set{parent: make([]int, n), rank: make([]int, n), count: n}
	for i := range s.parent {
		s.parent[i] = i
	}
	return s
}

func (s *Set) Find(x int) (int, error) {
	if s == nil {
		return 0, ErrNilSet
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if x < 0 || x >= len(s.parent) {
		return 0, ErrBadIndex
	}
	root := x
	s.hops = 0
	for root != s.parent[root] {
		root, s.hops = s.parent[root], s.hops+1
	}
	for x != root {
		x, s.parent[x] = s.parent[x], root
	}
	return root, nil
}

func (s *Set) Union(x, y int) (bool, error) {
	if s == nil {
		return false, ErrNilSet
	}
	rx, ex := s.Find(x)
	ry, ey := s.Find(y)
	if ex != nil || ey != nil {
		return false, errors.Join(ex, ey)
	}
	if rx == ry {
		return false, nil
	}
	if s.rank[rx] < s.rank[ry] {
		rx, ry = ry, rx
	}
	s.parent[ry] = rx
	if s.rank[rx] == s.rank[ry] {
		s.rank[rx]++
	}
	s.count--
	return true, nil
}

func (s *Set) Connected(x, y int) (bool, error) {
	if s == nil {
		return false, ErrNilSet
	}
	rx, ex := s.Find(x)
	ry, ey := s.Find(y)
	return ex == nil && ey == nil && rx == ry, errors.Join(ex, ey)
}

func (s *Set) Count() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}
func (s *Set) LastFindHops() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.hops
	return n
}
