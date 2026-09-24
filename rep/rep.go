// Package rep 定义单副本条目、版本比较与 winner 判定。不依赖其他包。
package rep

// Entry 是单副本持有的条目；Empty 为 true 表示无条目（不是版本 0）。
type Entry struct {
	Value string
	Ver   int64
	Empty bool
}

// Beats 报告 a 是否比 b 更新：非空胜空；版本高者胜；同版本取字典序更大的 value。
func Beats(a, b Entry) bool {
	if a.Empty != b.Empty {
		return b.Empty // a 非空而 b 空时 a 胜
	}
	if a.Empty {
		return false // 都为空，无胜负
	}
	if a.Ver != b.Ver {
		return a.Ver > b.Ver
	}
	return a.Value > b.Value
}

// Winner 在 entries 中判定胜者，返回胜者条目与其下标；全部为空时 ok=false。
func Winner(entries []Entry) (w Entry, idx int, ok bool) {
	idx = -1
	for i, e := range entries {
		if e.Empty {
			continue
		}
		if idx == -1 || Beats(e, w) {
			w, idx = e, i
		}
	}
	return w, idx, idx != -1
}

// NeedsRepair 报告 e 相对 winner 是否需要回填（为空、版本落后或同版本值冲突）。
func NeedsRepair(e, w Entry) bool {
	return e.Empty || e.Ver < w.Ver || (e.Ver == w.Ver && e.Value != w.Value)
}
