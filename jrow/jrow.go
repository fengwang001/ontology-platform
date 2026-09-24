// Package jrow 维护单 key 的 join 状态，并据此产出 + / - 变更日志条目。
package jrow

// Change 是一条变更日志条目：Op 为 '+'（新增/替换）或 '-'（撤回）。
type Change struct {
	Op  byte
	Key string
	LV  int
	RV  int
}

// WideRow 是宽表中的一行。
type WideRow struct {
	Key string
	LV  int
	RV  int
}

// Row 是单 key 在两张表里的当前状态。
// probes 记录最近一次 Locate 为定位该 key 进行的 map 查找次数，
// 非导出，不出现在任何公开接口的返回值语义里。
type Row struct {
	InL    bool
	LV     int
	InR    bool
	RV     int
	probes int
}

// Locate 在 l、r 两张表里定位 key，返回其当前状态。
// 只按 key 直接查 map，查找次数恒为 2，与表规模无关。
func Locate(l, r map[string]int, key string) Row {
	lv, inL := l[key]
	rv, inR := r[key]
	return Row{InL: inL, LV: lv, InR: inR, RV: rv, probes: 2}
}

// PutL 返回写入 L[key]=lv 之后的新状态与应输出的变更日志。
// 调用方须保证非重复更新（旧值不等于 lv 或 key 原本不在 L）。
func (r Row) PutL(key string, lv int) (Row, []Change) {
	old := r
	r.InL, r.LV = true, lv
	if !r.InR {
		return r, nil
	}
	var chs []Change
	if old.InL { // 已匹配 key 的值更新：先撤回旧宽表行
		chs = append(chs, Change{Op: '-', Key: key, LV: old.LV, RV: r.RV})
	}
	return r, append(chs, Change{Op: '+', Key: key, LV: lv, RV: r.RV})
}

// PutR 与 PutL 对称。
func (r Row) PutR(key string, rv int) (Row, []Change) {
	old := r
	r.InR, r.RV = true, rv
	if !r.InL {
		return r, nil
	}
	var chs []Change
	if old.InR {
		chs = append(chs, Change{Op: '-', Key: key, LV: r.LV, RV: old.RV})
	}
	return r, append(chs, Change{Op: '+', Key: key, LV: r.LV, RV: rv})
}

// DelL 返回删除 L[key] 之后的新状态与应输出的变更日志。
// 调用方须保证 r.InL 为真。
func (r Row) DelL(key string) (Row, []Change) {
	var chs []Change
	if r.InR {
		chs = append(chs, Change{Op: '-', Key: key, LV: r.LV, RV: r.RV})
	}
	r.InL, r.LV = false, 0
	return r, chs
}

// DelR 与 DelL 对称。
func (r Row) DelR(key string) (Row, []Change) {
	var chs []Change
	if r.InL {
		chs = append(chs, Change{Op: '-', Key: key, LV: r.LV, RV: r.RV})
	}
	r.InR, r.RV = false, 0
	return r, chs
}
