// Package grid 封装网格整体：根格、插入删除、并发控制与跳过计数。
package grid

import (
	"sync"

	"ontology/cell"
	"ontology/geom"
)

// Grid 是可分裂二维网格索引。查询与插入通过读写锁互斥，
// 查询永远看到某个一致的快照。
type Grid struct {
	mu      sync.RWMutex
	root    *cell.Cell
	skipped uint64
}

// New 创建覆盖 bounds、单格容量为 capacity 的空网格。
func New(bounds geom.Rect, capacity int) *Grid {
	return &Grid{root: cell.New(bounds, capacity)}
}

// NewWithRoot 用已有子树作为根格（持久化读回时用）。
func NewWithRoot(root *cell.Cell) *Grid {
	return &Grid{root: root}
}

// Insert 插入点；坐标为 NaN 或 ±Inf 时拒绝并计入 Skipped。
func (g *Grid) Insert(p geom.Point) bool {
	if !p.Valid() {
		g.mu.Lock()
		g.skipped++
		g.mu.Unlock()
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.root.Bounds.Contains(p) {
		g.skipped++
		return false
	}
	g.root.Insert(p, 0)
	return true
}

// Delete 删除一个坐标相同的点。
func (g *Grid) Delete(p geom.Point) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.root.Delete(p)
}

// Contains 报告点是否在网格中。
func (g *Grid) Contains(p geom.Point) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.root.Contains(p)
}

// ReadView 在持读锁期间对根格执行 fn，供查询与持久化遍历。
func (g *Grid) ReadView(fn func(root *cell.Cell)) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	fn(g.root)
}

// Skipped 返回因坐标非法或越界被拒绝的插入次数。
func (g *Grid) Skipped() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.skipped
}

// Total 返回网格内点总数。
func (g *Grid) Total() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.root.Total()
}

// CellCount 返回网格的格总数。
func (g *Grid) CellCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.root.Count()
}
