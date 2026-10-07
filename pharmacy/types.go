package pharmacy

const (
	// MaxTime 是逻辑时钟上限（分钟）。
	MaxTime = 10_000_000
	// MaxQty 是单次数量上限。
	MaxQty = 1_000_000
	// MaxLines 是单张处方的最大药品行数。
	MaxLines = 8
	// ValidityMinutes 是处方有效期：开具后 72 小时（分钟计）。
	ValidityMinutes = 72 * 60
)

// Config 是引擎配置。
type Config struct {
	// R 为预留取药窗口（分钟）。预留自起始时刻起 R 分钟内（含第 R 分钟）有效。
	R int
}

// LineInput 是处方一行的输入。
type LineInput struct {
	DrugID string
	Qty    int
}

// RxInput 是处方受理输入。
type RxInput struct {
	ID         string
	Patient    string
	IssueTime  int
	WholeOrder bool
	Lines      []LineInput
}

// RxStatus 是处方状态。
type RxStatus string

const (
	RxActive    RxStatus = "ACTIVE"    // 有效
	RxCompleted RxStatus = "COMPLETED" // 已完成
	RxExpired   RxStatus = "EXPIRED"   // 已过期
	RxCancelled RxStatus = "CANCELLED" // 已取消
)

// LineStatus 是处方行状态。
type LineStatus string

const (
	LineBackordered LineStatus = "BACKORDERED" // 欠药中（无预留）
	LinePartial     LineStatus = "PARTIAL"     // 部分预留，仍有欠药
	LineReserved    LineStatus = "RESERVED"    // 已预留，无欠药
	LineFulfilled   LineStatus = "FULFILLED"   // 需求已满足且已发放
	LineVoid        LineStatus = "VOID"        // 已作废（处方过期/取消）
)

// LineState 是处方一行的查询结果。
type LineState struct {
	DrugID    string
	Demand    int
	Reserved  int
	Dispensed int
	Backorder int
	Status    LineStatus
}

// RxState 是处方查询结果。
type RxState struct {
	ID         string
	Patient    string
	IssueTime  int
	WholeOrder bool
	Status     RxStatus
	Lines      []LineState
}

// DrugState 是药品查询结果。
type DrugState struct {
	ID             string
	BoxSize        int
	Splittable     bool
	OnHand         int // 在库量（含被预留部分）
	Reserved       int // 当前有效预留量
	Available      int // 可用量 = 在库量 - 有效预留量
	BackorderTotal int // 欠药总量
}
