// Package phase 定义迁移三阶段及其转移合法性：只能向前、不能跳。
package phase

// Phase 是迁移阶段。
type Phase int

const (
	Normal    Phase = iota // 只写 v1、只读 v1
	DualWrite              // 双写 v1+v2、读 v1
	Switched               // 只写 v2、只读 v2
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

// CanBegin 判定 Normal → DualWrite 是否合法。
func (p Phase) CanBegin() bool { return p == Normal }

// CanSwitch 判定 DualWrite → Switched 是否合法。
func (p Phase) CanSwitch() bool { return p == DualWrite }

// Begin 返回 BeginMigration 后的阶段；非法时 ok=false。
func (p Phase) Begin() (next Phase, ok bool) {
	if !p.CanBegin() {
		return p, false
	}
	return DualWrite, true
}

// Switch 返回 Switch 后的阶段；非法时 ok=false。
func (p Phase) Switch() (next Phase, ok bool) {
	if !p.CanSwitch() {
		return p, false
	}
	return Switched, true
}
