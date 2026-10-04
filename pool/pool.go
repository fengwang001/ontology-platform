// Package pool 维护英雄池状态：各英雄是否仍可被禁用或选取。
package pool

// Pool 记录编号 1..h 的英雄的可用状态，
// 并用后继并查集在近乎 O(1) 时间内定位最小可用英雄。
type Pool struct {
	h       int
	avail   []bool // avail[i]：英雄 i 是否可用（下标 1..h，h+1 为哨兵）
	parent  []int  // 后继并查集：已删除 i 的 parent[i] 指向不小于 i 的最小可用英雄
	touched int    // 非导出计数器：累计读取的英雄池记录数
}

// New 返回含 H 个英雄（全部可用）的池。
func New(h int) *Pool {
	p := &Pool{h: h, avail: make([]bool, h+2), parent: make([]int, h+2)}
	for i := 1; i <= h+1; i++ {
		p.avail[i] = true
		p.parent[i] = i
	}
	return p
}

// get 读取一条并查集记录并计数。
func (p *Pool) get(i int) int {
	p.touched++
	return p.parent[i]
}

// find 返回不小于 x 的最小可用英雄编号，路径折半压缩。
// 可用英雄恒为根（parent 指向自身），故链长不超过已删除英雄数。
func (p *Pool) find(x int) int {
	for {
		v := p.get(x)
		if v == x {
			return x
		}
		g := p.get(v)
		p.parent[x] = g // 写不计入读取
		x = g
	}
}

// Available 报告英雄是否既未被禁也未被选。
func (p *Pool) Available(hero int) bool {
	p.touched++
	return p.avail[hero]
}

// remove 删除英雄：调用方须保证其当前可用。
func (p *Pool) remove(hero int) {
	p.avail[hero] = false
	p.parent[hero] = p.find(hero + 1)
}

// Ban 将英雄标记为已禁。
func (p *Pool) Ban(hero int) { p.remove(hero) }

// Pick 将英雄标记为已选。
func (p *Pool) Pick(hero int) { p.remove(hero) }

// MinAvailable 返回当前可用英雄的最小编号。
func (p *Pool) MinAvailable() int { return p.find(1) }

// Touched 返回累计读取的英雄池记录数。
func (p *Pool) Touched() int { return p.touched }

// Size 返回英雄总数 H。
func (p *Pool) Size() int { return p.h }
