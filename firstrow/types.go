package firstrow

import "fmt"

// Change 表示变更流中的一条变更。
type Change struct {
	Op        Op
	Key       string
	ID        string
	EventTime int64
}

// Op 为变更操作类型。
type Op int

const (
	Insert Op = iota + 1
	Retract
)

// Output 是首条变化时产生的一条下游输出。
type Output struct {
	Kind      OutputKind
	Key       string
	ID        string
	EventTime int64
}

// OutputKind 为输出类型：先撤回旧首条，再写入新首条。
type OutputKind int

const (
	Upsert OutputKind = iota + 1
	Delete
)

// RejectReason 标识一条变更被拒绝的可区分原因。
type RejectReason int

const (
	ReasonEmptyKey RejectReason = iota + 1
	ReasonEmptyID
	ReasonDuplicateID
	ReasonMissingID
	ReasonTooManyRows
	ReasonInvalidOp
)

// RejectedChangeError 描述一批变更中第一条非法变更及其原因。
type RejectedChangeError struct {
	Index  int
	Reason RejectReason
}

func (e *RejectedChangeError) Error() string {
	return fmt.Sprintf("change at index %d rejected: %s", e.Index, e.Reason)
}

func (r RejectReason) String() string {
	switch r {
	case ReasonEmptyKey:
		return "empty key"
	case ReasonEmptyID:
		return "empty id"
	case ReasonDuplicateID:
		return "insert of a live id"
	case ReasonMissingID:
		return "retract of a non-existent id"
	case ReasonTooManyRows:
		return "live row count exceeds the per-key limit"
	case ReasonInvalidOp:
		return "unknown operation"
	default:
		return fmt.Sprintf("unknown reject reason %d", int(r))
	}
}

func (o Op) String() string {
	switch o {
	case Insert:
		return "INSERT"
	case Retract:
		return "RETRACT"
	default:
		return fmt.Sprintf("OP(%d)", int(o))
	}
}

func (k OutputKind) String() string {
	switch k {
	case Upsert:
		return "UPSERT"
	case Delete:
		return "DELETE"
	default:
		return fmt.Sprintf("KIND(%d)", int(k))
	}
}
