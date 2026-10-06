package settlement

// order.go —— 指令与状态的公开类型定义。

// Status 为指令生命周期状态。
type Status int

const (
	StatusPending     Status = iota // 待交割：尚未发生任何交割
	StatusPartial                   // 部分交割：已交割一部分，剩余仍在滚动
	StatusComplete                  // 已完成：全额交割
	StatusForceClosed               // 已强制了结：剩余量因累计失败达到 B 日而作废
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusPartial:
		return "partial"
	case StatusComplete:
		return "complete"
	case StatusForceClosed:
		return "force_closed"
	default:
		return "unknown"
	}
}

// Order 是指令的不可变输入参数。
type Order struct {
	ID           int64
	Security     int64
	Buyer        string
	Seller       string
	Qty          int64
	Price        int64
	SettleDay    int64
	AllowPartial bool
}

// OrderView 是查询返回的指令状态快照。
type OrderView struct {
	Order
	DeliveredQty int64
	FailedDays   int
	Status       Status
}

// AccountView 是查询返回的账户头寸与应付应收快照。
type AccountView struct {
	Name           string
	Securities     map[int64]int64
	Cash           int64
	FeePayable     int64
	FeeReceivable  int64
	CompPayable    int64
	CompReceivable int64
}
