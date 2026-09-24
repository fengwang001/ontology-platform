// Package phase 定义迁移三阶段及其转移合法性：只能向前、不能跳。
package phase

// Phase 是迁移阶段：Normal → DualWrite → Switched。
type Phase int

const (
	Normal Phase = iota
	DualWrite
	Switched
)

func (p Phase) String() string {
	switch p {
	case Normal:
		return "Normal"
	case DualWrite:
		return "DualWrite"
	case Switched:
		return "Switched"
	}
	return "Unknown"
}

// Next 返回 p 的唯一合法后继；已至终态时 ok 为 false。
// 阶段只能向前一步，不允许后退或跨越。
func (p Phase) Next() (next Phase, ok bool) {
	switch p {
	case Normal:
		return DualWrite, true
	case DualWrite:
		return Switched, true
	}
	return p, false
}
