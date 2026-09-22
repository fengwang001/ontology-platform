package cell

import "ontology/geom"

// MaxDepth 限制分裂深度，防止浮点不可再分时无限递归。
const MaxDepth = 48

// Cell 是网格树节点：叶格存点；分裂后点下沉到四个子格。
type Cell struct {
	Bounds geom.Rect
	Depth  int
	Split  bool
	Pts    []geom.Point
	Kids   [4]*Cell // 0=左下 1=右下 2=左上 3=右上
}

// New 创建覆盖 bounds、深度 depth 的空叶格。
func New(bounds geom.Rect, depth int) *Cell { return nil }

// Insert 向本格子树插入点；cap 为叶容量，超限时触发分裂。
func (c *Cell) Insert(p geom.Point, cap int) {}

// Delete 按 id 从子树删除点，报告是否删到。
func (c *Cell) Delete(id uint64) bool { return false }

// Count 返回子树全部点数。
func (c *Cell) Count() int { return 0 }

// Find 按 id 在子树中查找点，报告是否存在。
func (c *Cell) Find(id uint64) (geom.Point, bool) { return geom.Point{}, false }

// mid 与 quadrant 返回分裂中点及点应去的子格下标。
func (c *Cell) mid() (mx, my float64)        { return 0, 0 }
func (c *Cell) quadrant(p geom.Point) int    { return 0 }
func (c *Cell) canSplit(mx, my float64) bool { return false }
