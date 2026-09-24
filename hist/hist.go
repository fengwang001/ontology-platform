// Package hist 维护单 key 的版本链：追加版本、按位点取最新可见版本、按 upto 收基线。
// 不依赖其他包。
package hist

import "sync/atomic"

// Val 是写入的值类型。
type Val = any

// Chain 是单 key 的版本链，seqs 严格递增，vals 与之一一对应。
// 不是并发安全的，并发控制由上层 store 负责。
type Chain struct {
	seqs []int64
	vals []Val
	// checked 记录最近一次 At 在链上检查过的版本个数，仅用于内部复杂度核验。
	// 读路径也会写它，故用 atomic 保证并发读下 -race 干净。
	checked atomic.Int32
}

// Append 追加一个版本，seq 必须大于链上所有已有 Seq。
func (c *Chain) Append(seq int64, v Val) {
	c.seqs = append(c.seqs, seq)
	c.vals = append(c.vals, v)
}

// At 返回 Seq<=s 的最新版本；没有则 ok=false。
// 按位点直接定位：s 越过链尾时直接取尾，否则二分，绝不从链头逐个扫。
func (c *Chain) At(s int64) (v Val, ok bool) {
	n := 1
	defer func() { c.checked.Store(int32(n)) }()
	length := len(c.seqs)
	if length == 0 {
		n = 0
		return nil, false
	}
	if s >= c.seqs[length-1] {
		return c.vals[length-1], true
	}
	if s < c.seqs[0] {
		return nil, false
	}
	lo, hi := 0, length-1 // 不变量：seqs[lo]<=s<seqs[hi]
	for lo+1 < hi {
		n++
		mid := int(uint(lo+hi) >> 1)
		if c.seqs[mid] <= s {
			lo = mid
		} else {
			hi = mid
		}
	}
	return c.vals[lo], true
}

// Compact 回收历史：保留 Seq<=upto 的最新版本作为基线（若存在），
// Seq>upto 的版本全部保留，更老版本全部丢弃。
func (c *Chain) Compact(upto int64) {
	i := 0 // 找第一个 Seq>upto 的下标
	for i < len(c.seqs) && c.seqs[i] <= upto {
		i++
	}
	if i == 0 {
		return // 没有 <=upto 的版本，链不变
	}
	// 基线是 i-1；保留 [i-1:]，即基线 + 全部更晚版本。
	c.seqs = append([]int64(nil), c.seqs[i-1:]...)
	c.vals = append([]Val(nil), c.vals[i-1:]...)
}
