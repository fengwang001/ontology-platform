// Package query 实现矩形范围查询：完全包含整取、相交逐点判、相离跳过。
package query

import (
	"ontology/cell"
	"ontology/geom"
	"ontology/grid"
)

// Stats 记录一次查询的访问统计，计数器非导出。
type Stats struct {
	cellsVisited  int
	pointsChecked int
}

// CellsVisited 返回访问过的格数。
func (s *Stats) CellsVisited() int { return s.cellsVisited }

// PointsChecked 返回逐点判定过的点数。
func (s *Stats) PointsChecked() int { return s.pointsChecked }

// Range 返回网格中落在查询矩形 r（左闭右开）内的全部点。
// st 非 nil 时累计访问统计；并发调用应各自传入独立的 Stats。
func Range(g *grid.Grid, r geom.Rect, st *Stats) []geom.Point {
	var out []geom.Point
	g.ReadView(func(root *cell.Cell) {
		walk(root, r, st, &out)
	})
	return out
}

func walk(c *cell.Cell, r geom.Rect, st *Stats, out *[]geom.Point) {
	if !c.Bounds.Intersects(r) {
		return // 相离：跳过
	}
	if st != nil {
		st.cellsVisited++
	}
	if r.ContainsRect(c.Bounds) {
		collect(c, out) // 完全包含：整取，不逐点判定
		return
	}
	if c.Divided {
		for _, ch := range c.Children {
			walk(ch, r, st, out)
		}
		return
	}
	for _, p := range c.Points { // 相交：逐点判定
		if st != nil {
			st.pointsChecked++
		}
		if r.Contains(p) {
			*out = append(*out, p)
		}
	}
}

// collect 整取子树内全部点，不做逐点判定。
func collect(c *cell.Cell, out *[]geom.Point) {
	*out = append(*out, c.Points...)
	for _, ch := range c.Children {
		if ch != nil {
			collect(ch, out)
		}
	}
}
