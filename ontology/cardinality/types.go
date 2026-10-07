package cardinality

// ReasonCode 是新建关联请求被拒绝的互斥原因编码。
type ReasonCode int

const (
	// ReasonNone 未被拒绝。
	ReasonNone ReasonCode = iota
	// ReasonBaselineConflict 基线版本落后：调用方读取链接类型时所依据的
	// 基线版本不等于约束作用域当前版本。该判定优先于一切名额判定。
	ReasonBaselineConflict
	// ReasonCommittedFull 当前已确认（committed）关联数已达到基数上限。
	// 该判定优先于"进行中名额占用"判定。
	ReasonCommittedFull
	// ReasonInflightOccupied 已确认数未达上限，但剩余名额被其他进行中的
	// 预留请求占用，属于暂时性拒绝。
	ReasonInflightOccupied
)

// Limit 是一个链接类型在单个基数约束作用域上的基数上限，必须为非负数。
type Limit int64

// ScopeKey 标识一条基数约束的作用域：链接类型 + 源实例 + 目标实例。
// 基数判定（含进行中名额占用）只在同一 ScopeKey 内相互影响。
type ScopeKey struct {
	LinkType string
	Source   string
	Target   string
}

// CounterSnapshot 是判定时刻可在不对外暴露任何额外可变状态的前提下、
// 随判定结果一并返回的"名额计算证据"。三个计数与剩余名额都是
// 同一临界区内的一致快照，可直接据此重放判定。
type CounterSnapshot struct {
	// Committed 已确认关联数（只增于成功提交，历史请求总数不计入）。
	Committed int64
	// Inflight 当时仍存活（未提交、未中止、未过期）的进行中预留数。
	Inflight int
	// Limit 基数上限。
	Limit int64
	// Remaining 判定瞬间剩余名额 = Limit - Committed - Inflight。
	Remaining int64
	// Seq 该作用域内的逻辑时钟值（每次成功提交 +1）。
	Seq int64
}

// Decision 记录一次 Reserve 请求的判定依据与结果。
// 被拒绝时，除生成该 Decision（审计证据）外，不得产生任何其他
// 外部可观察状态变化。
type Decision struct {
	Scope         ScopeKey
	Accepted      bool
	Reason        ReasonCode
	Snapshot      CounterSnapshot
	ExpectedV     int64
	ObservedV     int64
	ReservationID string
	// Seq 是管理器级全局逻辑时钟，为每次 Reserve 尝试（含拒绝）
	// 分配严格单调的判定序号，供审计重放确定等价串行顺序。
	Seq int64
	// NowNanos 判定时刻（取自注入时钟，保证测试可重放）。
	NowNanos int64
}

// FinalizationOutcome 描述 Commit/Abort/心跳的结果。
type FinalizationOutcome int

const (
	OutcomeNoop FinalizationOutcome = iota
	OutcomeCommitted
	OutcomeAborted
	OutcomeRefreshed
)

// EventKind 标识审计日志记录的事件类别。
type EventKind int

const (
	EventReserve EventKind = iota
	EventCommit
	EventAbort
	EventExpire
)

// Event 是审计日志中的一条完整记录。拒绝只产生 EventReserve，
// 不产生任何关联/版本/时钟变化。
type Event struct {
	Kind          EventKind
	Seq           int64
	Scope         ScopeKey
	ReservationID string
	Decision      *Decision
	Committed     int64
	Inflight      int
	NowNanos      int64
}
