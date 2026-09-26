// Package lc 最少连接核心：连接数切片与最小堆定位，不依赖其他包。
package lc

// Core 维护各服务器活动连接数，用 (count, index) 字典序最小堆定位最少者，
// 堆顶即「连接数最少、并列下标最小」的服务器。
type Core struct {
	counts []int
	heap   []int // 服务器下标，按 (counts[s], s) 字典序堆序
	pos    []int // pos[s] = 服务器 s 在 heap 中的位置
	checks int   // 最近一次 Pick 为定位最少者检查的服务器个数（非导出，复杂度证据）
}

// New 构造 n 台服务器的核心，初始连接数均为 0。
func New(n int) *Core {
	c := &Core{counts: make([]int, n), heap: make([]int, n), pos: make([]int, n)}
	for i := range c.heap {
		c.heap[i] = i
		c.pos[i] = i
	}
	return c
}

// Pick 返回活动连接数最少、并列取下标最小的服务器下标。只看堆顶，O(1)。
func (c *Core) Pick() int {
	c.checks = 1
	return c.heap[0]
}

// Incr 将服务器 i 的连接数加一，并下沉维护堆序。
func (c *Core) Incr(i int) {
	c.counts[i]++
	c.down(c.pos[i])
}

// Decr 将服务器 i 的连接数减一，并上浮维护堆序。调用方保证不下溢。
func (c *Core) Decr(i int) {
	c.counts[i]--
	c.up(c.pos[i])
}

// Count 返回服务器 i 当前连接数。
func (c *Core) Count(i int) int { return c.counts[i] }

// less 比较堆中位置 a、b 上的服务器：先比连接数，并列比下标。
func (c *Core) less(a, b int) bool {
	sa, sb := c.heap[a], c.heap[b]
	if c.counts[sa] != c.counts[sb] {
		return c.counts[sa] < c.counts[sb]
	}
	return sa < sb
}

func (c *Core) swap(a, b int) {
	c.heap[a], c.heap[b] = c.heap[b], c.heap[a]
	c.pos[c.heap[a]] = a
	c.pos[c.heap[b]] = b
}

func (c *Core) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !c.less(i, p) {
			break
		}
		c.swap(i, p)
		i = p
	}
}

func (c *Core) down(i int) {
	for {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(c.heap) && c.less(l, m) {
			m = l
		}
		if r < len(c.heap) && c.less(r, m) {
			m = r
		}
		if m == i {
			break
		}
		c.swap(i, m)
		i = m
	}
}
