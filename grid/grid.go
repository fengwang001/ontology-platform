package grid

import (
	"sync"

	"ontology/cell"
	"ontology/geom"
)

// Grid 是整棵可分裂网格树，所有方法并发安全。
type Grid struct {
	mu      sync.RWMutex
	root    *cell.Cell
	bounds  geom.Rect
	cap     int
	skipped uint64
}

// New 创建覆盖 bounds、叶容量为 cap 的空网格。
func New(bounds geom.Rect, cap int) *Grid { return nil }

// Insert 插入点；NaN/Inf/越界被拒绝并计入跳过数。
func (g *Grid) Insert(p geom.Point) bool { return false }

// Delete 按 id 删除点。
func (g *Grid) Delete(id uint64) bool { return false }

// Find 按 id 点查。
func (g *Grid) Find(id uint64) (geom.Point, bool) { return geom.Point{}, false }

// Count 返回已提交点总数。
func (g *Grid) Count() int { return 0 }

// Skipped 返回被拒绝（NaN/Inf/越界）的插入次数。
func (g *Grid) Skipped() uint64 { return 0 }

// Root 与 Bounds 供查询包只读遍历。
func (g *Grid) Root() *cell.Cell  { return nil }
func (g *Grid) Bounds() geom.Rect { return geom.Rect{} }
