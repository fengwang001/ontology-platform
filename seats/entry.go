package seats

// Status 是条目的生命周期状态：预占中 -> 已出票 / 已取消。
// “已过期”不是独立状态，而是 (StatusHold 且 当前时刻 >= Expiry) 的导出判定。
type Status int

const (
	StatusHold      Status = iota // 预占中（可能已过期）
	StatusTicketed                // 已出票，占用永不过期
	StatusCancelled               // 已取消，占用已释放
)

func (st Status) String() string {
	switch st {
	case StatusHold:
		return "hold"
	case StatusTicketed:
		return "ticketed"
	case StatusCancelled:
		return "cancelled"
	}
	return "unknown"
}

// Leg 是行程中的一个航段及其指定的舱位等级。Class 取值 0..NumClasses-1，
// 0 为最高等级。
type Leg struct {
	Segment string
	Class   int
}

// Entry 是一条预占或已出票记录。旅客人数创建后不可变更，取消总是整条行程。
type Entry struct {
	ID     string
	Status Status
	Legs   []Leg
	Pax    int
	Expiry int64 // 预占到期的绝对时刻；当前时刻 >= Expiry 即视为已过期
}
