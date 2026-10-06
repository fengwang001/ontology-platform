package permit

import "fmt"

// Time 是离散时间轴上的时刻，所有时段均为左闭右开 [Start, End)。
type Time int64

// Priority 为许可优先级。
type Priority int

const (
	// Regular 常规许可：受同路段、绕行、走廊上限三类规则约束。
	Regular Priority = iota
	// Emergency 应急抢修许可：仅校验参数与同路段车道数，可抢占常规许可。
	Emergency
)

func (p Priority) valid() bool { return p == Regular || p == Emergency }

// Status 是许可在某一时刻的生命周期状态。
type Status int

const (
	// StatusApproved 已批（含顺延重审通过后带新时段）。
	StatusApproved Status = iota
	// StatusPreempted 被应急许可抢占，等待顺延重审。
	StatusPreempted
	// StatusPendingReschedule 抢占顺延重审未通过，待重排（不会自动重试）。
	StatusPendingReschedule
	// StatusRevoked 申请人撤销，不再参与任何判定。
	StatusRevoked
)

func (s Status) String() string {
	switch s {
	case StatusApproved:
		return "APPROVED"
	case StatusPreempted:
		return "PREEMPTED"
	case StatusPendingReschedule:
		return "PENDING_RESCHEDULE"
	case StatusRevoked:
		return "REVOKED"
	default:
		return fmt.Sprintf("STATUS(%d)", int(s))
	}
}

// Interval 是左闭右开的时段 [Start, End)。
type Interval struct {
	Start Time
	End   Time
}

func (iv Interval) valid() bool { return iv.Start < iv.End }

// overlap 判断两左闭右开时段是否相交；首尾相接（a.End == b.Start）不算相交。
func (iv Interval) overlap(o Interval) bool {
	return iv.Start < o.End && o.Start < iv.End
}

// Segment 是一条路段。Detour 为其指定绕行路线（其他路段的 ID 序列）。
type Segment struct {
	ID       string
	Lanes    int
	Corridor string
	Detour   []string
}

// Network 是路网静态配置。
type Network struct {
	Segments []Segment
}

// Permit 是一份占道施工许可的完整档案。
type Permit struct {
	ID       string
	Segment  string
	Lanes    int // 封闭车道数
	Priority Priority

	// Current 为当前生效时段；被抢占/待重排时保留为被抢占时刻的残余候选时段。
	Current Interval

	Status Status

	// ApprovedAt 为该许可首次被接受的操作时刻。
	ApprovedAt Time
	// ApproveOrder 为全局单调递增的批准次序，用于抢占顺延的重排次序。
	ApproveOrder int64

	// Original 为最近一次已批时段（延期重审时用于排除自身）。
	Original Interval

	// PreemptedBy 为造成最近一次抢占的应急许可 ID。
	PreemptedBy string

	// Corridor 为路段所属走廊（冗余自路网，便于走廊索引维护）。
	Corridor string

	// indexedStart 为该许可当前在 treap 中的键（插入时的 Current.Start）。
	// 抢占顺延后 Current.Start 会改变，摘除索引必须按插入时的旧键进行。
	indexedStart Time

	// History 为不可变状态历史，相同操作序列重放必得相同结果。
	History []HistoryEntry
}

// HistoryEntry 记录一次被接受操作造成的状态变更或事件。
type HistoryEntry struct {
	At       Time     // 操作时刻
	Kind     string   // ACCEPT / EXTEND / REVOKE / PREEMPT / RESCHEDULE_APPROVED / RESCHEDULE_REJECTED
	Priority Priority // 许可优先级
	Interval Interval // 事件后该许可的当前时段
	Detail   string   // 人类与朴素模型可共用的判定依据
	By       string   // 抢占方应急许可 ID（PREEMPT 时）
}

// ApplyRequest 为受理申请。
type ApplyRequest struct {
	OpAt     Time
	ID       string
	Segment  string
	Lanes    int
	Start    Time
	End      Time
	Priority Priority
}

// ExtendRequest 为延期申请；NewEnd 为新的截止时刻。
type ExtendRequest struct {
	OpAt   Time
	ID     string
	NewEnd Time
}

// RevokeRequest 为撤销申请。
type RevokeRequest struct {
	OpAt Time
	ID   string
}

// QueryRequest 查询某路段在某时刻的封闭情况。
type QueryRequest struct {
	OpAt    Time // 未使用：查询不推进时钟，仅用于日志/并发排序
	Segment string
	At      Time
}

// QueryResult 为查询结果。
type QueryResult struct {
	Segment   string
	At        Time
	Closed    int      // 该时刻封闭车道总数
	ActiveIDs []string // 生效许可 ID（按批准次序）
}

// OperationKind 标识操作类型。
type OperationKind int

const (
	OpApply OperationKind = iota
	OpExtend
	OpRevoke
)

func (k OperationKind) String() string {
	switch k {
	case OpApply:
		return "APPLY"
	case OpExtend:
		return "EXTEND"
	case OpRevoke:
		return "REVOKE"
	default:
		return "UNKNOWN"
	}
}

// OperationRecord 是一条被接受的状态变更操作的归档（供审计/重放与日志）。
type OperationRecord struct {
	Seq    int64
	Kind   OperationKind
	At     Time
	Apply  *ApplyRequest
	Extend *ExtendRequest
	Revoke *RevokeRequest
	Note   string // 处理与判定依据（如抢占了谁、谁顺延失败等）
}

// AcceptResult 为受理结果。
type AcceptResult struct {
	ID       string
	Accepted bool
	Err      *RuleError

	// 仅应急许可受理时非空：被抢占的常规许可 ID（按批准次序）。
	PreemptedIDs []string
	// 抢占顺延重审结果：许可 ID -> 是否恢复为已批。
	Reschedule map[string]bool
	// 被抢占后恢复的新时段。
	RescheduledInterval map[string]Interval
}
