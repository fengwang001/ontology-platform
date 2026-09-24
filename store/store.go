// Package store 维护连续、有序、互不重叠的分区列表（Insert/Delete/Locate/Compact）。依赖 part。
package store

import (
	"errors"
	"math/bits"
	"sync"
	"sync/atomic"

	"ontology/part"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrInvalidParams = errors.New("store: 参数非法")
	ErrOutOfRange    = errors.New("store: 键越界")
	ErrNotFound      = errors.New("store: 键不存在")
)

type Range = part.Range // 分区区间与负载的只读快照
type Store struct {
	low, high                      int64
	splitThreshold, mergeThreshold int
	parts                          []*part.Part
	mu                             sync.RWMutex
	lastCompares                   atomic.Int64 // 非导出：最近一次 Locate 比较过的分区数，不进公开接口
}

// New 校验参数并构造只含一个分区 [low,high) 的 Store。
func New(low, high int64, splitThreshold, mergeThreshold int) (*Store, error) {
	if low < 0 || low >= high || splitThreshold < 2 || mergeThreshold < 1 || splitThreshold <= mergeThreshold {
		return nil, ErrInvalidParams
	}
	return &Store{
		low: low, high: high, splitThreshold: splitThreshold, mergeThreshold: mergeThreshold,
		parts: []*part.Part{part.New(low, high)},
	}, nil
}

// search 二分定位 key 所在分区下标并返回比较过的分区个数（调用方已持锁）。
func (s *Store) search(key int64) (idx, compares int) {
	lo, hi := 0, len(s.parts) // parts[lo].Lo()<=key<parts[hi].Lo()，hi 越界视为 +inf
	for lo+1 < hi {
		m := (lo + hi) / 2
		compares++
		if s.parts[m].Lo() <= key {
			lo = m
		} else {
			hi = m
		}
	}
	return lo, compares
}

// Insert：load 达阈值立即在中点分裂一次（非递归）；重复插入幂等；越界不留痕。
func (s *Store) Insert(key int64) error {
	if key < s.low || key >= s.high {
		return ErrOutOfRange
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i, _ := s.search(key)
	p := s.parts[i]
	if p.Has(key) {
		return nil
	}
	p.Add(key)
	if p.Load() >= s.splitThreshold {
		l, r := p.Split()
		s.parts = append(s.parts, nil)
		copy(s.parts[i+2:], s.parts[i+1:])
		s.parts[i], s.parts[i+1] = l, r
	}
	return nil
}

// Delete 从 key 所在分区移除 key；键不存在（含越界）整体失败，状态不变。
func (s *Store) Delete(key int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key < s.low || key >= s.high {
		return ErrNotFound
	}
	i, _ := s.search(key)
	if !s.parts[i].Has(key) {
		return ErrNotFound
	}
	s.parts[i].Take(key)
	return nil
}

// Locate 返回 key 所在分区的区间快照；键越界返回 ErrOutOfRange。可并发。
func (s *Store) Locate(key int64) (Range, error) {
	if key < s.low || key >= s.high {
		return Range{}, ErrOutOfRange
	}
	s.mu.RLock()
	i, n := s.search(key)
	p := s.parts[i]
	rg := Range{Lo: p.Lo(), Hi: p.Hi(), Load: p.Load()}
	s.mu.RUnlock()
	s.lastCompares.Store(int64(n))
	return rg, nil
}

// Ranges 返回按 lo 升序的分区区间快照。可并发。
func (s *Store) Ranges() []Range {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Range, len(s.parts))
	for i, p := range s.parts {
		out[i] = Range{Lo: p.Lo(), Hi: p.Hi(), Load: p.Load()}
	}
	return out
}

// Compact 从左到右贪心单遍；Mergeable 内部校验相邻，绝不合并非相邻分区。
func (s *Store) Compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*part.Part, 0, len(s.parts))
	for i := 0; i < len(s.parts); i++ {
		cur := s.parts[i]
		for i+1 < len(s.parts) && cur.Mergeable(s.parts[i+1], s.mergeThreshold) {
			cur.Merge(s.parts[i+1])
			i++
		}
		out = append(out, cur)
	}
	s.parts = out
}
func (s *Store) numParts() int { s.mu.RLock(); defer s.mu.RUnlock(); return len(s.parts) }

// CheckLocateLogBound 铺出 100/1000/10000 个分区各做一次 Locate，
// 比较分区数不超过 ceil(log2(P))+1 时返回 true；数值不经公开接口暴露。
func CheckLocateLogBound() bool {
	for _, P := range []int{100, 1000, 10000} {
		s, _ := New(0, 1<<62, 2, 1)
		for k := int64(0); s.numParts() < P; k++ {
			if s.Insert(k*2654435761+12345) != nil {
				return false
			}
		}
		if _, err := s.Locate(12345); err != nil ||
			s.lastCompares.Load() > int64(bits.Len(uint(s.numParts()-1))+1) {
			return false
		}
	}
	return true
}
