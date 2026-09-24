// Package dg 实现 digest：质心切片 + exact 真实值多重集。依赖 cent 包。
package dg

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/cent"
)

// ErrRetractMissing 撤回的值不在 exact 多重集中。
var ErrRetractMissing = errors.New("dg: retract value not in exact set")

// Digest 近似分位数摘要。lastVisits 是非导出计数器（最近一次 Quantile
// 访问的质心个数），不出现在任何公开接口里。
type Digest struct {
	mu         sync.Mutex
	k          int
	cs         []cent.Centroid
	exact      map[float64]int
	n          int
	lastVisits int
}

// New 建立预算为 k 的空 digest（k ≥ 1 由 api 层校验）。
func New(k int) *Digest { return &Digest{k: k, exact: map[float64]int{}} }

// add 把 {v,1} 并入质心列表，重排并压缩；不触碰 exact/n（由调用方维护）。
func (d *Digest) add(v float64) {
	d.cs = append(d.cs, cent.Centroid{Mean: v, Count: 1})
	cent.Sort(d.cs)
	d.cs = cent.Compress(d.cs, d.k)
}

// Add 加入一个值：质心侧与 exact 多重集同步增长。
func (d *Digest) Add(v float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.add(v)
	d.exact[v]++
	d.n++
}

// Merge 把 o 的全部质心并入 d（o 完全不变），按 d 的 k 压缩。
func (d *Digest) Merge(o *Digest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d == o {
		o = d.cloneLocked()
	} else {
		o.mu.Lock()
		defer o.mu.Unlock()
	}
	d.cs = append(d.cs, o.cs...)
	cent.Sort(d.cs)
	d.cs = cent.Compress(d.cs, d.k)
	for v, c := range o.exact {
		d.exact[v] += c
	}
	d.n += o.n
}

// Retract 先整体校验（任一值不存在则整体拒绝、不留痕），统一扣减后
// 从剩余 exact 升序逐个重 Add，整体重算质心。
func (d *Digest) Retract(vs ...float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	need := map[float64]int{}
	for _, v := range vs {
		need[v]++
	}
	for v, c := range need {
		if d.exact[v] < c {
			return ErrRetractMissing
		}
	}
	for v, c := range need {
		if d.exact[v] -= c; d.exact[v] == 0 {
			delete(d.exact, v)
		}
		d.n -= c
	}
	keys := make([]float64, 0, len(d.exact))
	for v := range d.exact {
		keys = append(keys, v)
	}
	sort.Float64s(keys)
	d.cs = d.cs[:0]
	for _, v := range keys {
		for i := 0; i < d.exact[v]; i++ {
			d.add(v)
		}
	}
	return nil
}

// Count 精确等于 exact 集合大小。
func (d *Digest) Count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}

// Quantile 估计分位数，并把本次访问的质心个数记入非导出计数器。
func (d *Digest) Quantile(q float64) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, visits, err := cent.Quantile(d.cs, q)
	d.lastVisits = visits
	return v, err
}

// Centroids 返回当前质心列表的副本（mean 升序）。
func (d *Digest) Centroids() []cent.Centroid {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]cent.Centroid(nil), d.cs...)
}

func (d *Digest) cloneLocked() *Digest {
	c := &Digest{k: d.k, n: d.n, cs: append([]cent.Centroid(nil), d.cs...), exact: make(map[float64]int, len(d.exact))}
	for v, n := range d.exact {
		c.exact[v] = n
	}
	return c
}

func equalCS(a, b []cent.Centroid) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SelfCheck 对内置确定性序列核验四条不变量与 visits 上界；全过返回 nil。
func SelfCheck() error {
	seq := func(n int) []float64 { // LCG 确定性伪随机，无第三方依赖
		out, x := make([]float64, n), 12345
		for i := range out {
			x = (x*1103515245 + 12345) & 0x7fffffff
			out[i] = float64(x % 1000)
		}
		return out
	}
	build := func(k int, vs []float64) *Digest {
		d := New(k)
		for _, v := range vs {
			d.Add(v)
		}
		return d
	}
	d1 := build(8, seq(500)) // 不变量1：误差 ≤ maxCount-1，Count 精确，质心 ≤ k
	if d1.n != 500 || len(d1.cs) > 8 {
		return fmt.Errorf("inv1: n=%d centroids=%d", d1.n, len(d1.cs))
	}
	vals := seq(500)
	sort.Float64s(vals)
	maxC := 0
	for _, c := range d1.cs {
		maxC = max(maxC, c.Count)
	}
	for qi := 0; qi <= 100; qi++ {
		v, err := d1.Quantile(float64(qi) / 100)
		if err != nil {
			return fmt.Errorf("inv1: %w", err)
		}
		lo := sort.SearchFloat64s(vals, v)
		hi := sort.Search(len(vals), func(i int) bool { return vals[i] > v }) - 1
		r := float64(qi) / 100 * float64(len(vals)-1)
		if dist := max(float64(lo)-r, r-float64(hi), 0); dist > float64(maxC-1) {
			return fmt.Errorf("inv1: q=%d%% rank error %v > %d", qi, dist, maxC-1)
		}
	}
	a, b := build(5, seq(200)), build(5, seq(120)) // 不变量2：合并自洽
	bc := b.cloneLocked()
	a.Merge(b)
	if a.n != 320 || b.n != 120 || !equalCS(b.cs, bc.cs) {
		return fmt.Errorf("inv2: count or o mutated")
	}
	a2, b2 := build(5, seq(200)), build(5, seq(120))
	b2.Merge(a2)
	if !equalCS(a.cs, b2.cs) {
		return fmt.Errorf("inv2: not commutative")
	}
	vs := seq(300) // 不变量3：撤回即重算
	d3 := build(4, vs)
	if err := d3.Retract(vs[0], vs[1]); err != nil {
		return fmt.Errorf("inv3: %w", err)
	}
	rest := append([]float64(nil), vs[2:]...)
	sort.Float64s(rest)
	if want := build(4, rest); !equalCS(d3.cs, want.cs) {
		return fmt.Errorf("inv3: retract != rebuild")
	}
	d4 := build(4, seq(100)) // 不变量4：失败不留痕
	snap := d4.cloneLocked()
	if d4.Retract(1e9) != ErrRetractMissing {
		return fmt.Errorf("inv4: retract-missing not rejected")
	}
	if _, err := d4.Quantile(2); err != cent.ErrQuantileRange {
		return fmt.Errorf("inv4: bad q not rejected")
	}
	if _, err := New(3).Quantile(0.5); err != cent.ErrEmpty {
		return fmt.Errorf("inv4: empty not rejected")
	}
	if d4.n != snap.n || len(d4.exact) != len(snap.exact) || !equalCS(d4.cs, snap.cs) {
		return fmt.Errorf("inv4: rejected op left trace")
	}
	for _, n := range []int{100, 1000, 10000} { // 第四节：visits ≤ k，不随 n 增长
		d := build(32, seq(n))
		if _, err := d.Quantile(0.5); err != nil {
			return err
		}
		if d.lastVisits > 32 {
			return fmt.Errorf("visits %d > k=32 (n=%d)", d.lastVisits, n)
		}
	}
	return nil
}
