// Package hist 维护单个 key 的版本链：至多一个基线版本加一段按 Seq 升序的活版本。
// 它不依赖工程内其他包；位点含义由调用方约定（Seq 越大越新，可见当且仅当 Seq <= s）。
package hist

import (
	"errors"
	"sort"
)

// Val 是一次写入携带的值，按不可变数据使用。
type Val any

// Version 是一次成功写入：全局连续递增的 Seq 与其值。
type Version struct {
	Seq int
	Val Val
}

// Chain 是单 key 的版本链。
//
// 不变量：base（若存在）的 Seq 严格小于 live 中每个版本的 Seq；
// live 按 Seq 严格升序；一次 Compact 之后，所有 Seq<=upto 的版本至多剩 base 一个。
// probe 为非导出计数器，记录最近一次 At 在链上检查过的版本个数。
type Chain struct {
	base    Version
	hasBase bool
	live    []Version
	probe   int
}

// Append 在活版本末尾追加一个版本。调用方（store）保证 seq 全局连续递增。
func (c *Chain) Append(seq int, v Val) {
	c.live = append(c.live, Version{Seq: seq, Val: v})
}

// At 返回位点 s 处该 key 最新可见版本的值；不存在任何 Seq<=s 的版本时 ok=false。
// 它是无副作用的纯读，可被多 goroutine 并发调用。
//
// 最新位点（s >= 末个活版本 Seq）直接定位末元素，只检查 1 个版本，
// 不从头扫链；更早的位点用二分在活版本上定位，再退到基线版本。
func (c *Chain) At(s int) (Val, bool) {
	v, ok, _ := c.find(s)
	return v, ok
}

// find 是纯定位：额外返回本次在链上检查过的版本个数（局部量，不写共享状态）。
func (c *Chain) find(s int) (v Val, ok bool, probe int) {
	if n := len(c.live); n > 0 {
		last := &c.live[n-1]
		probe++ // 检查末个活版本
		if last.Seq <= s {
			return last.Val, true, probe // 直取最新版本：与链长无关的常数步
		}
		// 末个活版本仍晚于 s：二分找第一个 Seq>s 的下标。
		i := sort.Search(n, func(i int) bool {
			probe++
			return c.live[i].Seq > s
		})
		if i > 0 {
			return c.live[i-1].Val, true, probe
		}
	}
	if c.hasBase {
		probe++ // 检查基线版本
		if c.base.Seq <= s {
			return c.base.Val, true, probe
		}
	}
	return nil, false, probe
}

// probeRead 与 At 返回相同结果，但把本次检查过的版本个数记入非导出字段 probe。
// 仅供包内（同包测试与导出闸门）做复杂度演示，不参与并发读路径。
func (c *Chain) probeRead(s int) (Val, bool) {
	v, ok, n := c.find(s)
	c.probe = n
	return v, ok
}

// probeCount 是非导出访问器，计数器的数值只能在包内（含同包测试）被读到。
func (c *Chain) probeCount() int { return c.probe }

// Compact 按 upto 收基线：Seq<=upto 的最新版本降为基线，更老版本丢弃；
// Seq>upto 的活版本全部保留。链上没有任何 Seq<=upto 的版本时链保持不变。
func (c *Chain) Compact(upto int) {
	i := sort.Search(len(c.live), func(i int) bool { return c.live[i].Seq > upto })
	if i > 0 {
		c.base = c.live[i-1] // 结构体拷贝，避免持有底层数组
		c.hasBase = true
	}
	c.live = append(c.live[:0], c.live[i:]...)
}

// QuickProbeOK 是导出的布尔闸门：它在包内核验「读最新位点只检查常数个版本」，
// 只返回通过与否，绝不把 probe 的具体数值放进任何公开签名。
// 跨 m=100..10000 逐档构造版本链，穿插噪声键后读目标键最新位点。
func QuickProbeOK() error {
	for _, m := range []int{100, 1000, 10000} {
		var target, noise Chain
		for i := 1; i <= m; i++ {
			target.Append(i, i)
			if i%3 == 0 {
				noise.Append(i, i) // 穿插其他 key 的写入
			}
		}
		v, ok := target.probeRead(m)
		if !ok || v != m || target.probeCount() > 2 {
			return errors.New("hist: latest read must touch a constant number of versions")
		}
	}
	return nil
}
