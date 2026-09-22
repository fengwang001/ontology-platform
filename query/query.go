package query

import (
	"ontology/cell"
	"ontology/geom"
)

// Counters 记录剪枝统计：访问格数与逐点判定点数。
type Counters struct {
	CellsVisited int
	PointsTested int
}

// Engine 在给定网格树上做矩形范围查询。
type Engine struct{}

// New 创建查询引擎。
func New() *Engine { return nil }

// Range 返回落在 r 内的点；相离剪枝、完全包含整取、相交逐点判。
func (e *Engine) Range(root *cell.Cell, r geom.Rect) ([]geom.Point, Counters) {
	return nil, Counters{}
}
