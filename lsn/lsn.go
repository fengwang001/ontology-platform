// Package lsn 维护一个有序且全局唯一的 LSN 记录集合：保序插入、
// 二分升序定位与空洞枚举。本包不依赖工程内任何其他包。
package lsn

import (
	"errors"
	"sort"
	"sync/atomic"
)

// 哨兵错误：两类非法写入，可被 errors.Is 判定，且互不相同。
var (
	ErrNegative  = errors.New("lsn: negative LSN is not allowed")
	ErrDuplicate = errors.New("lsn: duplicate LSN breaks unique addressing")
)

// Set 是升序、唯一的 LSN 集合。vals 始终升序，set 负责 O(1) 查重。
// probe 为非导出计数器，记录最近一次查询定位比较过的记录个数；
// 它不出现在任何导出接口中，仅同包内部测试可直接读取。原子类型：
// 只读路径（api 持 RLock 并发查询）也会更新它，必须与并发读兼容。
type Set struct {
	vals  []int64
	exist map[int64]struct{}
	probe atomic.Int64
}

// New 返回空集合。
func New() *Set {
	return &Set{vals: []int64{}, exist: make(map[int64]struct{})}
}

// Add 加入一条 LSN。负数或重复均被整体拒绝：先校验、后写入，
// 任何被拒操作都不改变集合与计数器。
func (s *Set) Add(v int64) error {
	if v < 0 {
		return ErrNegative
	}
	if _, ok := s.exist[v]; ok {
		return ErrDuplicate
	}
	i := sort.Search(len(s.vals), func(i int) bool { return s.vals[i] >= v })
	s.vals = append(s.vals, 0)
	copy(s.vals[i+1:], s.vals[i:])
	s.vals[i] = v
	s.exist[v] = struct{}{}
	return nil
}

// Len 返回记录数。
func (s *Set) Len() int { return len(s.vals) }

// AtOK 返回升序第 i 个 LSN（i 越界时 ok=false）。
// 按下标直接取值，定位无需逐条比较。
func (s *Set) AtOK(i int) (int64, bool) {
	if i < 0 || i >= len(s.vals) {
		return 0, false
	}
	return s.vals[i], true
}

// IndexOf 返回 v 的升序名次（0 起）。不存在时 ok=false，rank 为按序插入点。
// 手写下界二分并累计 probe：每次迭代只与一条记录比较。
func (s *Set) IndexOf(v int64) (rank int, ok bool) {
	s.probe.Store(0)
	p := 0
	lo, hi := 0, len(s.vals)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		p++
		if s.vals[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	s.probe.Store(int64(p))
	if lo < len(s.vals) && s.vals[lo] == v {
		return lo, true
	}
	return lo, false
}

// Holes 升序枚举闭区间 [min,max] 内不是已存在 LSN 的整数。
// min 之前的整数（日志尚未开始）不算空洞；记录数 <2 时为空。
// 不预分配：结果规模本就等于空洞数，且可避开 max-min+1 的 int64 溢出。
func (s *Set) Holes() []int64 {
	n := len(s.vals)
	out := []int64{}
	if n < 2 {
		return out
	}
	for i := 1; i < n; i++ {
		for v := s.vals[i-1] + 1; v < s.vals[i]; v++ {
			out = append(out, v)
		}
	}
	return out
}
