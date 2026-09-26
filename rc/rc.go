// Package rc 实现旋转卡壳：O(n) 枚举对跖点对并求凸多边形直径。
package rc

import (
	"sync/atomic"

	"ontology/cal"
)

// Caliper 持有凸多边形顶点（逆时针），可并发只读求直径。
type Caliper struct {
	poly  []cal.Point
	count atomic.Int64 // 非导出计数器：最近一次 Diameter 实际求过 d² 的顶点对个数
}

// New 复制顶点切片，调用方后续修改不影响本实例。
func New(poly []cal.Point) *Caliper {
	p := make([]cal.Point, len(poly))
	copy(p, poly)
	return &Caliper{poly: p}
}

// Diameter 返回最大平方距离与全部取得该值的对跖点对（下标对，小下标在前，去重）。
// 固定边 (v[i],v[i+1])，只要 area(j+1) > area(j) 就推进 j（模 n），
// 每条边贡献对跖点对 (i,j) 与 (i+1,j)；d² 并列最大者全部保留。
func (c *Caliper) Diameter() (int64, [][2]int) {
	p := c.poly
	n := len(p)
	j := 1
	for cal.Area2(p[0], p[1], p[(j+1)%n]) > cal.Area2(p[0], p[1], p[j]) {
		j = (j + 1) % n
	}
	best := int64(-1)
	var pairs [][2]int
	seen := make(map[[2]int]bool)
	var cnt int64
	for i := 0; i < n; i++ {
		a, b := p[i], p[(i+1)%n]
		for cal.Area2(a, b, p[(j+1)%n]) > cal.Area2(a, b, p[j]) {
			j = (j + 1) % n
		}
		for _, k := range [2]int{i, (i + 1) % n} {
			d := cal.Dist2(p[k], p[j])
			cnt++
			key := [2]int{k, j}
			if key[0] > key[1] {
				key[0], key[1] = key[1], key[0]
			}
			switch {
			case d > best:
				best, pairs = d, [][2]int{key}
				seen = map[[2]int]bool{key: true}
			case d == best && !seen[key]:
				seen[key] = true
				pairs = append(pairs, key)
			}
		}
	}
	c.count.Store(cnt)
	return best, pairs
}
