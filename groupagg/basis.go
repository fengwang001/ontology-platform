package groupagg

import "fmt"

// 本文件为每次接受的操作生成“判定依据”说明（Basis），
// 与输入操作、输出条目一起进入诊断日志，便于人工核对增量算法的决策。

func basisInsert(g string, old, after GroupAgg) string {
	if old.Count == 0 {
		return fmt.Sprintf("INSERT: group %q absent -> add row, new agg {sum=%d,count=%d}", g, after.Sum, after.Count)
	}
	return fmt.Sprintf("INSERT: group %q old {sum=%d,count=%d} -> new {sum=%d,count=%d}",
		g, old.Sum, old.Count, after.Sum, after.Count)
}

func basisSameGroup(g string, oldVal, newVal int64, old, after GroupAgg) string {
	return fmt.Sprintf("UPDATE same group %q: retract value %d, add value %d, count unchanged %d; {sum=%d} -> {sum=%d}",
		g, oldVal, newVal, old.Count, old.Sum, after.Sum)
}

func basisRekey(oldG, newG string, oldAgg, afterOld, newAgg, afterNew GroupAgg) string {
	oldTail := "kept"
	if afterOld.Count == 0 {
		oldTail = "removed (count reached 0)"
	}
	newHead := "existing"
	if newAgg.Count == 0 {
		newHead = "absent"
	}
	return fmt.Sprintf("UPDATE rekey %q -> %q: old group {sum=%d,count=%d} %s -> {sum=%d,count=%d}; "+
		"new group %s {sum=%d,count=%d} -> {sum=%d,count=%d}; emit old group before new group",
		oldG, newG, oldAgg.Sum, oldAgg.Count, oldTail, afterOld.Sum, afterOld.Count,
		newHead, newAgg.Sum, newAgg.Count, afterNew.Sum, afterNew.Count)
}

func basisDelete(g string, old, after GroupAgg) string {
	if after.Count == 0 {
		return fmt.Sprintf("DELETE: group %q {sum=%d,count=%d} -> count 0, removed from view", g, old.Sum, old.Count)
	}
	return fmt.Sprintf("DELETE: group %q {sum=%d,count=%d} -> {sum=%d,count=%d}",
		g, old.Sum, old.Count, after.Sum, after.Count)
}
