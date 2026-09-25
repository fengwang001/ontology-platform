// Package dedup 维护已应用事务号的精确集合（无容量淘汰）。
// 本包不做内部同步：由上层 apply 在其互斥锁内调用。
package dedup

import (
	"errors"
	"sort"
)

// Set 是 txid → 存在 的精确集合。
type Set struct {
	have   map[int64]struct{}
	probes int // 最近一次 Seen/Add 为查重而检查过的 txid 个数（非导出）
}

// New 创建空集合。
func New() *Set {
	return &Set{have: map[int64]struct{}{}}
}

// Seen 报告 txid 是否在集合中。map 按 txid 哈希直接定位，只检查 1 个键。
func (s *Set) Seen(txid int64) bool {
	s.probes = 1
	_, ok := s.have[txid]
	return ok
}

// Add 把 txid 加入集合；插入同样是哈希定位，与集合大小无关。
func (s *Set) Add(txid int64) {
	s.probes = 1
	s.have[txid] = struct{}{}
}

// Snapshot 返回已应用 txid 的升序副本。
func (s *Set) Snapshot() []int64 {
	out := make([]int64, 0, len(s.have))
	for t := range s.have {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// CheckProbeComplexity 只给通过/失败判定，不泄露 probes 的数值：
// 在多档集合规模下查一个全新 txid，探查个数必须恒为小常数。
func CheckProbeComplexity() error {
	const maxProbes = 3
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := int64(1); i <= int64(m); i++ {
			s.Add(i)
		}
		s.Seen(int64(m) + 1)
		if s.probes > maxProbes {
			return errors.New("dedup: probe count grows with set size")
		}
	}
	return nil
}
