package rollup

import (
	"sort"
)

// 确定性键序：层级 → 维度（先区分“是否为空值”，再按字符串），
// 保证视图、重放与批量重算的输出顺序可复现。
func sortKeys(keys []GroupKey) {
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		if a.HasDim1 != b.HasDim1 {
			return !a.HasDim1 // false（占位/无该维度）在前
		}
		if a.Dim1 != b.Dim1 {
			return a.Dim1 < b.Dim1
		}
		if a.HasDim2 != b.HasDim2 {
			return !a.HasDim2
		}
		return a.Dim2 < b.Dim2
	})
}

func snapshotGroups(m map[GroupKey]*groupState) []GroupView {
	out := make([]GroupView, 0, len(m))
	for k, g := range m {
		out = append(out, GroupView{Key: k, Count: g.count, Sum: g.sum})
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := out[i].Key, out[j].Key
		if ki.HasDim1 != kj.HasDim1 {
			return !ki.HasDim1
		}
		if ki.Dim1 != kj.Dim1 {
			return ki.Dim1 < kj.Dim1
		}
		if ki.HasDim2 != kj.HasDim2 {
			return !ki.HasDim2
		}
		return ki.Dim2 < kj.Dim2
	})
	return out
}

func sortRows(rows []Row) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
}

func groupsEqual(a, b map[GroupKey]*groupState) bool {
	if len(a) != len(b) {
		return false
	}
	for k, ga := range a {
		gb, ok := b[k]
		if !ok || *ga != *gb {
			return false
		}
	}
	return true
}
