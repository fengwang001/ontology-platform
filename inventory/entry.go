package inventory

// Leg 行程中的一个航段及其指定舱位等级。Class 取值 0(最高) 1 2(最低)。
type Leg struct {
	Segment string
	Class   int
}

// State 条目状态。已取消区分“预占被取消”与“出票后被取消”，
// 因为前者仍受预占到期时刻约束（拒绝次序中已过期优先于状态不符），
// 后者已确认、永不过期。
type State int

const (
	StateHold State = iota
	StateConfirmed
	StateCancelledHold
	StateCancelledTicket
)

func (s State) String() string {
	switch s {
	case StateHold:
		return "预占中"
	case StateConfirmed:
		return "已出票"
	case StateCancelledHold, StateCancelledTicket:
		return "已取消"
	}
	return "未知状态"
}

// expiredAt 报告条目在时刻 now 是否受“预占已过期”约束。
// 已确认（含出票后取消）的条目永不过期。
func (s State) expires() bool {
	return s == StateHold || s == StateCancelledHold
}

// entry 一条预占/已出票/已取消的记录。条目一旦创建永不删除，
// 以便“已过期”“已取消”“已出票”与“不存在”可区分。
type entry struct {
	id     uint64
	legs   []Leg
	pax    int
	expiry uint64
	state  State
	// hotIdx[i] 是该条目在第 i 个航段对应舱位的热表下标，
	// 用于出票/取消时 O(1) 移除占用。
	hotIdx []int
}
