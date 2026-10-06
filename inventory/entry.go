package inventory

// EntryState 条目状态：预占中 / 已出票 / 已取消。
// “已过期”不是状态，而是由 当前时刻 >= 到期时刻 派生。
type EntryState int

const (
	StateHeld EntryState = iota
	StateTicketed
	StateCancelled
)

func (s EntryState) String() string {
	switch s {
	case StateHeld:
		return "held"
	case StateTicketed:
		return "ticketed"
	case StateCancelled:
		return "cancelled"
	}
	return "unknown"
}

// Leg 行程中的一个航段及其指定舱位等级。
type Leg struct {
	Segment string `json:"segment"`
	Class   Class  `json:"class"`
}

// Entry 一次预占及其后续状态。旅客人数预占后不可变更，
// 不支持部分取消，取消总是整条行程。
type Entry struct {
	ID     string
	Legs   []Leg
	Party  int
	State  EntryState
	Expiry int64 // 预占到期时刻；出票后不再有意义（已确认占用永不过期）
}
