// Package resv 是加权蓄水池 A-Res 内核：键 u^(1/W)、排名全序
// （键大优先，键相等先到优先）、堆顶为排名最低者的小顶堆与替换判定。
// 只用标准库；输入合法性由上层 api 包负责。
package resv

import (
	"math"
	"sort"
)

// Entry 是对外可见的池中元素快照。
type Entry struct {
	ID  string
	W   float64
	Key float64
}

type node struct {
	id  string
	w   float64
	key float64
	seq int // 到达序号，越小越早；键相等时早到者排名更高
}

// Pool 是容量为 k 的蓄水池；不加锁，并发由 api 串行化。
type Pool struct {
	k, arrive int
	h         []node
	// visits 记录最近一次 Offer 单个元素时的池内比较次数（与堆顶、上浮、
	// 下沉的每次比较）。非导出，仅供包内白盒测试读取，不经导出方法外传数值。
	visits int
}

// New 创建容量 k 的蓄水池（k 合法性由 api 校验）。
func New(k int) *Pool { return &Pool{k: k} }

// Len 返回当前池中元素个数。
func (p *Pool) Len() int { return len(p.h) }

// ComplexityOK 供演示固定校验：m∈{100,1000,10000} 满池后，低键新元素比较
// 次数 ≤3（与 m 无关常数）、高键新元素 ≤2⌈log2 m⌉+4。内部读非导出 visits，
// 但只回是/否、不接受上界参数也不返回数值。精确断言见 TestHeapVisitBound。
func ComplexityOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		fill := func(uAt func(i int) float64) *Pool {
			q := New(m)
			for i := 0; i < m; i++ {
				q.Offer("fill", 1, uAt(i))
			}
			return q
		}
		q := fill(func(i int) float64 { return float64(m-i) / float64(m+1) })
		q.Offer("lo", 1, 1.0/float64(2*(m+1))) // 低于满池最低键 1/(m+1)
		if q.visits > 3 {
			return false
		}
		q2 := fill(func(i int) float64 { return 0.5 + 0.4*float64(i+1)/float64(m+1) })
		q2.Offer("hi", 1, 0.999999) // 键高于池中最高者，替换根后一路下沉
		if q2.visits > 2*ceilLog2(m)+4 {
			return false
		}
	}
	return true
}

func ceilLog2(m int) int {
	c, p := 0, 1
	for p < m {
		p, c = p<<1, c+1
	}
	return c
}

// rankLess 报告 a 排名是否严格低于 b：键小者低；键相等晚到者低。
func rankLess(a, b node) bool {
	if a.key != b.key {
		return a.key < b.key
	}
	return a.seq > b.seq
}

// Offer 处理一个已通过上层校验的元素，返回它是否最终在池中。键 u^(1/W)；
// 池满且新元素不严格高于堆顶时不替换（键相等晚到永不替换早到）。
func (p *Pool) Offer(id string, w, u float64) bool {
	p.visits = 0
	nd := node{id: id, w: w, key: math.Pow(u, 1.0/w), seq: p.arrive}
	p.arrive++
	if len(p.h) < p.k {
		p.push(nd)
		return true
	}
	p.visits++ // 与堆顶（排名最低者）比较一次
	if !rankLess(p.h[0], nd) {
		return false
	}
	p.h[0] = nd
	p.sink(0)
	return true
}

func (p *Pool) push(nd node) {
	p.h = append(p.h, nd)
	i := len(p.h) - 1
	for i > 0 {
		par := (i - 1) / 2
		p.visits++ // 上浮：与父节点比较
		if !rankLess(p.h[i], p.h[par]) {
			break
		}
		p.h[i], p.h[par] = p.h[par], p.h[i]
		i = par
	}
}

func (p *Pool) sink(i int) {
	n := len(p.h)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		lo := i
		p.visits++ // 与左孩子比较
		if rankLess(p.h[l], p.h[lo]) {
			lo = l
		}
		if r := l + 1; r < n {
			p.visits++ // 与右孩子比较
			if rankLess(p.h[r], p.h[lo]) {
				lo = r
			}
		}
		if lo == i {
			return
		}
		p.h[i], p.h[lo] = p.h[lo], p.h[i]
		i = lo
	}
}

// Entries 按排名从高到低返回池中元素副本（键相等时早到者在前）。
func (p *Pool) Entries() []Entry {
	cp := append([]node(nil), p.h...)
	sort.Slice(cp, func(i, j int) bool { return rankLess(cp[j], cp[i]) })
	out := make([]Entry, len(cp))
	for i, nd := range cp {
		out[i] = Entry{ID: nd.id, W: nd.w, Key: nd.key}
	}
	return out
}
