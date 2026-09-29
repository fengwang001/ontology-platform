package fulljoin

import "sort"

// View 返回当前物化视图的确定性快照（按键、左 ID、右 ID 排序，空位排末尾）。
// 并发只读同一实例时，多次调用得到的切片逐字段相同。
func (m *Maintainer) View() []OutRow {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]OutRow, 0, len(m.out))
	for o := range m.out {
		out = append(out, cloneOutRow(o))
	}
	sortOutRows(out)
	return out
}

func cloneOutRow(o OutRow) OutRow {
	cp := OutRow{Key: o.Key}
	if o.Left != nil {
		l := *o.Left
		cp.Left = &l
	}
	if o.Right != nil {
		r := *o.Right
		cp.Right = &r
	}
	return cp
}

func sortOutRows(rs []OutRow) {
	id := func(r *Row) string {
		if r == nil {
			return ""
		}
		return r.ID
	}
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if id(a.Left) != id(b.Left) {
			return id(a.Left) < id(b.Left)
		}
		return id(a.Right) < id(b.Right)
	})
}
