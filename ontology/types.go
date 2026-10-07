package ontology

// ScopeKey 标识一个基数约束实例的作用域：链接类型 + 受约束端对象。
// 例如 linkType=Employee.department, field=employees, object=dept-1，
// 表示该部门雇员集合必须满足声明的基数上限。
type ScopeKey struct {
	LinkType string
	Field    string
	ObjectID string
}

// Outcome 表示一次新建关联请求的最终结果。
type Outcome int

const (
	// OutcomePending 进行中，尚未最终确定。
	OutcomePending Outcome = iota
	// OutcomeCommitted 已确认成功，关联真正建立。
	OutcomeCommitted
	// OutcomeRolledBack 已确认失败（显式回滚）。
	OutcomeRolledBack
	OutcomeExpired
)

func (o Outcome) String() string {
	switch o {
	case OutcomePending:
		return "PENDING"
	case OutcomeCommitted:
		return "COMMITTED"
	case OutcomeRolledBack:
		return "ROLLED_BACK"
	case OutcomeExpired:
		return "EXPIRED"
	default:
		return "UNKNOWN"
	}
}

// RejectReason 互斥的拒绝原因（成功时为 RejectNone）。
type RejectReason int

const (
	RejectNone RejectReason = iota
	// RejectBaselineConflict 基线版本落后，判定优先级最高。
	RejectBaselineConflict
	// RejectConfirmedFull 已确认关联数已达上限。
	RejectConfirmedFull
	// RejectInFlight 其他进行中请求占用名额导致的暂时性拒绝。
	RejectInFlight
	// RejectDuplicate 目标关联已存在，不应被重复计数。
	RejectDuplicate
)

func (r RejectReason) String() string {
	switch r {
	case RejectNone:
		return "NONE"
	case RejectBaselineConflict:
		return "BASELINE_CONFLICT"
	case RejectConfirmedFull:
		return "CONFIRMED_FULL"
	case RejectInFlight:
		return "IN_FLIGHT"
	case RejectDuplicate:
		return "DUPLICATE"
	default:
		return "UNKNOWN"
	}
}
