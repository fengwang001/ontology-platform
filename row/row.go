// Package row 维护单 Key 的多列视图：每列一个独立 cell，墓碑导致缺席，兄弟列互不影响。
package row

import "ontology/cell"

// Row 是一个 Key 下的全部列。checked 记录最近一次 Put/Del 为定位列
// 而检查过的列条目个数（map 定位应为常数），仅供包内测试观察。
type Row struct {
	cols    map[string]cell.Cell
	checked int
}

func New() *Row {
	return &Row{cols: make(map[string]cell.Cell)}
}

// apply 定位 (col) 并合并一次写/删，返回是否发生冲突。
func (r *Row) apply(col string, ts int64, val string, tomb bool) bool {
	r.checked = 1 // map 定位：恰好检查 1 个条目，与列总数无关
	next, conflict := cell.Apply(r.cols[col], ts, val, tomb)
	r.cols[col] = next
	return conflict
}

// Put 写入一列，返回是否发生冲突。
func (r *Row) Put(col string, ts int64, val string) bool {
	return r.apply(col, ts, val, false)
}

// Del 给一列写墓碑，返回是否发生冲突。
func (r *Row) Del(col string, ts int64) bool {
	return r.apply(col, ts, "", true)
}

// View 返回当前有值的列；被删（墓碑）与从未写入的列都缺席。
func (r *Row) View() map[string]string {
	out := make(map[string]string, len(r.cols))
	for col, c := range r.cols {
		if c.Set && !c.Tomb {
			out[col] = c.Val
		}
	}
	return out
}
