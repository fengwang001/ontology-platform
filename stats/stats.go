// Package stats 按 Key 管理多组 Welford 统计量。依赖 ws，单向。
package stats

import (
	"errors"
	"sync"

	"ontology/ws"
)

// 可判定的哨兵错误，三者（及自合并）互不相同。
var (
	ErrEmptyKey      = errors.New("stats: key 为空串")
	ErrValueNotFound = errors.New("stats: 组内不存在该值")
	ErrEmptyGroup    = errors.New("stats: 合入的组为空")
	ErrSelfMerge     = errors.New("stats: 组不能与自身合并")
)

// Kind 是操作类型。
type Kind int

const (
	OpAdd Kind = iota
	OpRemove
	OpMerge
)

// Op 描述一次 Apply 操作。Merge 时 Other 为来源组 Key。
type Op struct {
	Kind  Kind
	Key   string
	Other string
	X     float64
}

// View 是一组统计量的快照。
type View struct {
	N        int64
	Mean     float64
	M2       float64
	Variance float64
	Std      float64
}

// group 是一组的状态：Welford 统计量 + 值→计数哈希表（O(1) 定位 Remove 目标）。
type group struct {
	w    ws.W
	vals map[float64]int
}

// Store 按 Key 管理多组，并发安全。
type Store struct {
	mu          sync.RWMutex
	groups      map[string]*group
	lastLookups int // 非导出：最近一次 Apply 对组内元素做哈希查找的次数
}

// New 创建一个空 Store。
func New() *Store { return &Store{groups: make(map[string]*group)} }

// Apply 应用一个操作。先完成全部校验再变更，任何拒绝都不留痕。
func (s *Store) Apply(op Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastLookups = 0
	if op.Key == "" {
		return ErrEmptyKey
	}
	switch op.Kind {
	case OpAdd:
		g := s.groups[op.Key]
		if g == nil {
			g = &group{vals: make(map[float64]int)}
			s.groups[op.Key] = g
		}
		g.w.Add(op.X)
		s.lastLookups++
		g.vals[op.X]++
	case OpRemove:
		g := s.groups[op.Key]
		if g == nil {
			return ErrValueNotFound
		}
		s.lastLookups++
		if g.vals[op.X] == 0 {
			return ErrValueNotFound
		}
		g.w.Remove(op.X)
		s.lastLookups++
		g.vals[op.X]--
		if g.w.Count() == 0 {
			delete(s.groups, op.Key)
		}
	case OpMerge:
		if op.Other == op.Key {
			return ErrSelfMerge
		}
		src := s.groups[op.Other]
		if src == nil || src.w.Count() == 0 {
			return ErrEmptyGroup
		}
		dst := s.groups[op.Key]
		if dst == nil {
			dst = &group{vals: make(map[float64]int)}
			s.groups[op.Key] = dst
		}
		dst.w.Merge(src.w)
		for v, c := range src.vals {
			dst.vals[v] += c
		}
	default:
		return errors.New("stats: 未知操作类型")
	}
	return nil
}

// View 返回 Key 对应组的快照；空组/不存在的 Key 返回全零。
func (s *Store) View(key string) View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g := s.groups[key]
	if g == nil {
		return View{}
	}
	return View{N: g.w.Count(), Mean: g.w.Mean(), M2: g.w.M2(), Variance: g.w.Variance(), Std: g.w.Std()}
}
