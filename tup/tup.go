// Package tup 提供 (Col1, Col2) 元组的可哈希表示，以及
// 「分组 key → 元组 → 引用计数」映射；本包不依赖其他包。
package tup

// T 是 (Col1 string, Col2 int) 元组的可哈希表示，可直接作为 map 键。
type T struct {
	C1 string
	C2 int
}

// group 保存一个分组下各元组的引用计数，以及该分组的 distinct 计数
// （引用计数大于 0 的元组个数）。
type group struct {
	refs     map[T]int
	distinct int
}

// Catalog 是「分组 → 元组 → 引用计数」映射。Catalog 自身不加锁；并发安全
// 由唯一持有它的上层包 dd 保证。
type Catalog struct {
	groups map[string]*group
}

// NewCatalog 创建空 Catalog。
func NewCatalog() *Catalog {
	return &Catalog{groups: make(map[string]*group)}
}

// Adjust 把 key 分组下元组 t 的引用计数调整 delta（调用方只传 +1/-1），
// 返回调整前、后的引用计数。计数经 0↔1 翻转时同步维护该分组 distinct：
// 0→1 时 distinct 加 1，1→0 时 distinct 减 1，其余不变。
// 降到 0 的元组从映射中删除，使计数与「活跃引用」严格一致。
func (c *Catalog) Adjust(key string, t T, delta int) (before, after int) {
	g := c.groups[key]
	if g == nil {
		g = &group{refs: make(map[T]int)}
		c.groups[key] = g
	}
	before = g.refs[t] // 不存在即为 0
	after = before + delta
	switch {
	case before == 0 && after == 1:
		g.distinct++
	case before == 1 && after == 0:
		g.distinct--
	}
	if after == 0 {
		delete(g.refs, t)
	} else {
		g.refs[t] = after
	}
	return before, after
}

// Distinct 返回 key 分组中引用计数大于 0 的元组个数；未知分组为 0。
func (c *Catalog) Distinct(key string) int {
	if g := c.groups[key]; g != nil {
		return g.distinct
	}
	return 0
}

// Total 返回所有分组 distinct 计数之和。
func (c *Catalog) Total() int {
	n := 0
	for _, g := range c.groups {
		n += g.distinct
	}
	return n
}

// RefCount 返回 key 分组下元组 t 的当前引用计数；未知为 0。
// 供上层做与批量重算的一致性核验。
func (c *Catalog) RefCount(key string, t T) int {
	if g := c.groups[key]; g != nil {
		return g.refs[t]
	}
	return 0
}
