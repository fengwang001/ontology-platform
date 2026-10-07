package cardinality

import "time"

// Direction 表示链接在某链接类型上的方向。
type Direction string

const (
	// Outgoing 表示从源对象出发的方向。
	Outgoing Direction = "outgoing"
	// Incoming 表示指向源对象的反方向。
	Incoming Direction = "incoming"
)

// LinkState 表示链接的生命周期状态。
type LinkState string

const (
	// StateActive 有效链接，参与基数判断。
	StateActive LinkState = "active"
	// StatePending 待处理链接：对查询可见、计入历史统计，
	// 但不参与任何依赖基数的后续判断。
	StatePending LinkState = "pending"
	// StateDeleted 已被最终物理删除的链接。
	StateDeleted LinkState = "deleted"
)

// BucketKey 唯一确定一个基数桶。
type BucketKey struct {
	LinkType  string
	Direction Direction
	SourceID  string
}

// Link 是一条已登记的链接。
type Link struct {
	ID       string
	LinkType string
	SourceID string
	TargetID string
	State    LinkState
	// Seq 为登记序号（全引擎单调递增），是确定性标记依据的主键。
	Seq int64
	// MarkedAtSeq 为最近一次被标记为超额时的引擎全序序号；
	// 为 0 表示当前不在 pending 状态。
	MarkedAtSeq int64
	// MarkBasis 记录进入待处理状态时的确定性标记依据快照，
	// 用于最终处置时核对，保证中间调整轨迹不影响去向。
	MarkBasis MarkBasis
}

// MarkBasis 是一次超额标记的确定性依据快照。
type MarkBasis struct {
	// LimitAtMark 标记发生时该方向的新基数上限。
	LimitAtMark int
	// RankAtMark 该链接在桶内 (seq, id) 全序中的名次（从 1 开始）。
	RankAtMark int
	// TotalAtMark 标记时桶内已登记（未删除）链接总数。
	TotalAtMark int
	// OrderedIDs 标记时桶内 (seq, id) 全序的完整链接 ID 序列，
	// 使同一组输入重复执行得到完全相同的可核对依据。
	OrderedIDs []string
}

// PendingView 是待处理链接对查询暴露的视图。
type PendingView struct {
	Link Link
	// Stale 恒为 true：待处理链接不具备有效基数地位。
	Stale bool
	// Reason 以人类可读方式说明为何 stale。
	Reason string
	// DependentsStale 列出因该链接待处理而被同步标记为暂时不可信
	// 的派生状态标识。
	DependentsStale []string
}

// CreateResult 为创建链接请求的结果。
type CreateResult struct {
	Accepted bool
	Link     *Link
	// RejectReason 在被拒绝时给出拒绝原因码。
	RejectReason Reason
	// OrderSeq 为该请求在引擎全序中的序号。
	OrderSeq int64
}

// Disposition 表示对待处理链接的显式处置意图。
type Disposition string

const (
	// DispositionDelete 要求最终删除。
	DispositionDelete Disposition = "delete"
	// DispositionRetain 要求保留（相应提升该方向有效上限）。
	DispositionRetain Disposition = "retain"
)

// FinalizeResult 是一次显式处置的结果。
type FinalizeResult struct {
	LinkID string
	// Kept 为 true 表示最终保留并恢复有效，false 表示最终删除。
	Kept bool
	// Applied 为实际执行的去向；对象被撤销时可能与请求意图不同。
	Applied Disposition
	// Reason 记录触发原因码（对象撤销优先于默认处理路径）。
	Reason Reason
	// OrderSeq 为该处置在引擎全序中的序号。
	OrderSeq int64
}

// SetLimitResult 是一次基数上限调整的结果。
type SetLimitResult struct {
	// Marked 为本次新标记为超额的链接，按 (seq, id) 全序升序。
	Marked []string
	// Restored 为本次从待处理恢复为有效的链接，顺序与标记顺序相反
	// （按标记时顺序的逆序）。
	Restored []string
	OrderSeq int64
}

// DerivedState 是依赖某条链接存在性的派生状态。
type DerivedState struct {
	ID      string
	LinkID  string
	Payload string
	Suspect bool
	// Cleared 为 true 表示其所依赖链接已最终删除，派生状态被清理。
	Cleared bool
}

// DerivedView 是派生状态的查询视图。
type DerivedView struct {
	State DerivedState
	// Fresh 表示当前可信；false 时调用方必须将其视为过期依据。
	Fresh bool
	// Notice 在不可信时明确告知调用方。
	Notice string
}

// AuditRecord 是每次处理留下的事后核对记录。
type AuditRecord struct {
	Seq     int64
	At      time.Time
	Kind    EventKind
	Bucket  BucketKey
	LinkID  string
	Reason  Reason
	Basis   *MarkBasis
	Outcome string
	Detail  string
}
