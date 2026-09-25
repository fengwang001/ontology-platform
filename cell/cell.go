// Package cell 实现单列 LWW：一个 (Key,Col) 的胜者（值或墓碑）、平局裁定与冲突检测。
package cell

// Cell 是某 (Key,Col) 当前的胜者。Set 为 false 表示该列从未有任何写入。
type Cell struct {
	Set  bool
	Tomb bool // 胜者是墓碑
	TS   int64
	Val  string // 仅当 Set && !Tomb 时有意义
}

// Apply 把一次写/删（ts、val、tomb）合并进 cur，返回新胜者与是否发生冲突。
// 规则：TS 大者胜；TS 相等时两值取字典序大者、墓碑压值、两墓碑等价。
// 平局且非幂等（两值不同，或值与墓碑相遇）记一次冲突。
func Apply(cur Cell, ts int64, val string, tomb bool) (Cell, bool) {
	if !cur.Set || ts > cur.TS {
		return Cell{Set: true, Tomb: tomb, TS: ts, Val: val}, false
	}
	if ts < cur.TS {
		return cur, false
	}
	// ts == cur.TS：平局裁定
	switch {
	case cur.Tomb && tomb:
		return cur, false // 两墓碑等价，无变化
	case cur.Tomb != tomb:
		// 值 vs 墓碑：墓碑胜，冲突
		return Cell{Set: true, Tomb: true, TS: ts}, true
	default:
		// 两值：字典序大者胜；相等则幂等无变化
		if val == cur.Val {
			return cur, false
		}
		if val > cur.Val {
			return Cell{Set: true, TS: ts, Val: val}, true
		}
		return cur, true
	}
}
