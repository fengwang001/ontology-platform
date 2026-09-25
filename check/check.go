// Package check 提供朴素参照实现（直接数组区间加），用于对照测试 segtree。
package check

// Naive 是直接对数组做区间加、区间求和的朴素参照。
type Naive struct{ a []int64 }

// NewNaive 返回 n 个元素（初始全 0）的朴素参照。
func NewNaive(n int) *Naive { return &Naive{a: make([]int64, max(n, 1))} }

// AddRange 把 [l, r] 每个元素加 delta。
func (v *Naive) AddRange(l, r int, delta int64) {
	for i := l; i <= r; i++ {
		v.a[i] += delta
	}
}

// SumRange 返回 [l, r] 的和。
func (v *Naive) SumRange(l, r int) (s int64) {
	for i := l; i <= r; i++ {
		s += v.a[i]
	}
	return s
}

// buggy 是「只在更新时 pushdown、查询不 pushdown」的错误线段树，
// 仅作为第三节推导的反例参照，由 TestQueryMustPushdown 钉住其行为。
type buggy struct{ sum, lazy []int64 }

func newBuggy(n int) *buggy { return &buggy{make([]int64, 4*n), make([]int64, 4*n)} }

func (b *buggy) apply(i, nl, nr int, d int64) { b.sum[i] += d * int64(nr-nl+1); b.lazy[i] += d }

func (b *buggy) add(i, nl, nr, l, r int, d int64) {
	if l <= nl && nr <= r {
		b.apply(i, nl, nr, d)
		return
	}
	m := (nl + nr) / 2
	if z := b.lazy[i]; z != 0 { // 只在更新时 pushdown
		b.apply(2*i, nl, m, z)
		b.apply(2*i+1, m+1, nr, z)
		b.lazy[i] = 0
	}
	if l <= m {
		b.add(2*i, nl, m, l, r, d)
	}
	if r > m {
		b.add(2*i+1, m+1, nr, l, r, d)
	}
	b.sum[i] = b.sum[2*i] + b.sum[2*i+1]
}

func (b *buggy) query(i, nl, nr, l, r int) (s int64) { // 查询不 pushdown
	if l <= nl && nr <= r {
		return b.sum[i]
	}
	m := (nl + nr) / 2
	if l <= m {
		s += b.query(2*i, nl, m, l, r)
	}
	if r > m {
		s += b.query(2*i+1, m+1, nr, l, r)
	}
	return s
}
