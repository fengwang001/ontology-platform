// Package cell 实现网格单元格：容量上限、分裂与点的重新归属。
package cell

import "ontology/geom"

// MaxDepth 是分裂深度上限，保证同坐标点不会导致无限递归。
const MaxDepth = 48

// 子格编号：bit0=x 半区（0 左 1 右），bit1=y 半区（0 下 1 上）。
const (
	SW = iota
	SE
	NW
	NE
)

// Cell 是一个单元格。分裂后点全部下沉到子格，自身不再持点。
type Cell struct {
	Bounds   geom.Rect
	Points   []geom.Point
	Divided  bool
	Children [4]*Cell

	capacity int
}

// New 创建容量为 capacity、范围为 b 的单元格。
func New(b geom.Rect, capacity int) *Cell {
	return &Cell{Bounds: b, capacity: capacity}
}

// Capacity 返回格的容量上限。
func (c *Cell) Capacity() int { return c.capacity }

// childIndex 计算点应归属的子格编号；分裂线上的点归右/上子格。
func (c *Cell) childIndex(p geom.Point) int {
	m := c.Bounds.Mid()
	idx := SW
	if p.X >= m.X {
		idx |= 1
	}
	if p.Y >= m.Y {
		idx |= 2
	}
	return idx
}

// Insert 插入点，超容量时在终止条件允许下分裂并重新归属。
func (c *Cell) Insert(p geom.Point, depth int) {
	if c.Divided {
		c.Children[c.childIndex(p)].Insert(p, depth+1)
		return
	}
	c.Points = append(c.Points, p)
	if len(c.Points) > c.capacity && depth < MaxDepth && c.Bounds.Splittable() {
		c.split(depth)
	}
}

// split 分裂为四个子格并把全部点下沉；不增不减，总数守恒。
func (c *Cell) split(depth int) {
	b := c.Bounds
	m := b.Mid()
	c.Children[SW] = New(geom.Rect{X0: b.X0, Y0: b.Y0, X1: m.X, Y1: m.Y}, c.capacity)
	c.Children[SE] = New(geom.Rect{X0: m.X, Y0: b.Y0, X1: b.X1, Y1: m.Y}, c.capacity)
	c.Children[NW] = New(geom.Rect{X0: b.X0, Y0: m.Y, X1: m.X, Y1: b.Y1}, c.capacity)
	c.Children[NE] = New(geom.Rect{X0: m.X, Y0: m.Y, X1: b.X1, Y1: b.Y1}, c.capacity)
	c.Divided = true
	pts := c.Points
	c.Points = nil
	for _, p := range pts {
		c.Children[c.childIndex(p)].Insert(p, depth+1)
	}
}

// leaf 返回包含坐标 p 的叶格。
func (c *Cell) leaf(p geom.Point) *Cell {
	for c.Divided {
		c = c.Children[c.childIndex(p)]
	}
	return c
}

// Contains 报告点 p 是否在树中（坐标级精确匹配）。
func (c *Cell) Contains(p geom.Point) bool {
	for _, q := range c.leaf(p).Points {
		if q == p {
			return true
		}
	}
	return false
}

// Delete 删除一个与 p 坐标相同的点，返回是否删除成功。
func (c *Cell) Delete(p geom.Point) bool {
	l := c.leaf(p)
	for i, q := range l.Points {
		if q == p {
			l.Points = append(l.Points[:i], l.Points[i+1:]...)
			return true
		}
	}
	return false
}

// Total 返回子树内的点总数。
func (c *Cell) Total() int {
	n := len(c.Points)
	for _, ch := range c.Children {
		if ch != nil {
			n += ch.Total()
		}
	}
	return n
}

// Count 返回子树内的格数。
func (c *Cell) Count() int {
	n := 1
	for _, ch := range c.Children {
		if ch != nil {
			n += ch.Count()
		}
	}
	return n
}

// Depth 返回子树最大深度（根为 0）。
func (c *Cell) Depth() int {
	d := 0
	for _, ch := range c.Children {
		if ch != nil {
			if cd := ch.Depth() + 1; cd > d {
				d = cd
			}
		}
	}
	return d
}
