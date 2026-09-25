// Package csync 维护时钟集合、排序端点、CountAt（二分定位）与共识区间。
// 依赖方向：csync -> marz。
package csync

import (
	"sort"
	"sync"

	"ontology/marz"
)

// Clock 是一台时钟：误差闭区间 [Offset-Err, Offset+Err]。
type Clock struct {
	ID          string
	Offset, Err int64
}

// 可判定哨兵错误（api 别名暴露同一组值，errors.Is 可判）。
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

var (
	ErrNegativeError = sentinelError("csync: clock error must be >= 0")
	ErrDuplicateID   = sentinelError("csync: duplicate clock id")
	ErrEmptyID       = sentinelError("csync: clock id must be non-empty")
	ErrInvalidF      = sentinelError("csync: require 0 <= f < K")
	ErrNoConsensus   = sentinelError("csync: no consensus interval")
)

// Set 是并发安全的时钟集合。
type Set struct {
	mu     sync.Mutex
	clocks []Clock
	ids    map[string]struct{}
	los    []int64 // 升序：各时钟下端点 offset-err
	his    []int64 // 升序：各时钟上端点 offset+err
	f      int
	// probes 记录最近一次 CountAt 二分定位检查过的端点个数（非导出，仅供同包白盒测试）。
	probes int
}

// NewSet 创建允许 f 台坏钟的空集合；f<0 立即拒绝。
func NewSet(f int) (*Set, error) {
	if f < 0 {
		return nil, ErrInvalidF
	}
	return &Set{ids: map[string]struct{}{}, f: f}, nil
}

// F 返回构造参数 f；Len 返回当前时钟数 K。
func (s *Set) F() int { return s.f }

func (s *Set) Len() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.clocks) }

// Add 追加一台时钟；任一校验失败都在改状态之前返回（失败不留痕）。
func (s *Set) Add(c Clock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case c.ID == "":
		return ErrEmptyID
	case c.Err < 0:
		return ErrNegativeError
	}
	if _, dup := s.ids[c.ID]; dup {
		return ErrDuplicateID
	}
	s.clocks = append(s.clocks, c)
	s.ids[c.ID] = struct{}{}
	s.los = insertSorted(s.los, c.Offset-c.Err)
	s.his = insertSorted(s.his, c.Offset+c.Err)
	return nil
}

func insertSorted(a []int64, v int64) []int64 {
	i := sort.Search(len(a), func(i int) bool { return a[i] > v })
	a = append(a, 0)
	copy(a[i+1:], a[i:])
	a[i] = v
	return a
}

// boundCount 二分返回 sorted 中 <=v（upper）或 <v（lower）的元素个数，每次比较计入 probes。
func boundCount(sorted []int64, v int64, upper bool, probes *int) int {
	lo, hi := 0, len(sorted)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		*probes++
		less := sorted[mid] < v
		if upper {
			less = sorted[mid] <= v
		}
		if less {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// CountAt 返回闭区间包含 t 的时钟个数：#(Lo<=t) - #(Hi<t)，两次二分，O(log K)。
func (s *Set) CountAt(t int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probes = 0
	return boundCount(s.los, t, true, &s.probes) - boundCount(s.his, t, false, &s.probes)
}

// Snapshot 返回时钟副本（加入顺序）。
func (s *Set) Snapshot() []Clock {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Clock, len(s.clocks))
	copy(out, s.clocks)
	return out
}

// Consensus 对当前全部时钟全量重跑扫描线（不变量 1：与朴素重算一致）。
func (s *Set) Consensus() (lo, hi int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := len(s.clocks)
	if k < 2 {
		return 0, 0, ErrNoConsensus
	}
	if s.f >= k {
		return 0, 0, ErrInvalidF
	}
	lo, hi, ok := marz.Sweep(buildEvents(s.clocks), k-s.f)
	if !ok {
		return 0, 0, ErrNoConsensus
	}
	return lo, hi, nil
}

// buildEvents 取全部时钟生成端点并排序（同位 L 在 R 之前，闭端点不丢共识）。
func buildEvents(cs []Clock) []marz.Event {
	ev := make([]marz.Event, 0, 2*len(cs))
	for _, c := range cs {
		l, h, _ := marz.MakeEvents(c.Offset, c.Err) // Add 已保证 Err>=0
		ev = append(ev, l, h)
	}
	marz.SortEvents(ev)
	return ev
}
