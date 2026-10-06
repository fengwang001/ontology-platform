// Package collab implements an offline-first collaborative document sync
// server with replay, out-of-order batches, transaction groups and
// field-level versions.
package collab

import "errors"

// Integer bounds for field values and deltas.
const (
	MinInt = -1_000_000_000_000_000 // -10^15
	MaxInt = +1_000_000_000_000_000 // +10^15
)

// RetainWindow is the number of most recent per-operation results kept for
// every (client, document) pair.
const RetainWindow = 1000

// MaxBatchOps is the maximum number of operations accepted in one Sync batch.
const MaxBatchOps = 500

// Kind declares the semantic kind of a document field.
type Kind uint8

const (
	// KindSet is an overwrite field. Its value may be absent and its field
	// version is 0 while absent.
	KindSet Kind = 1
	// KindAdd is an accumulator field. It always exists and starts at 0.
	KindAdd Kind = 2
)

// OpType distinguishes the two operation types.
type OpType uint8

const (
	OpSet OpType = 1
	OpAdd OpType = 2
)

// ResultKind is the per-operation verdict.
type ResultKind uint8

const (
	ResApplied      ResultKind = 1 // 已应用
	ResMerged       ResultKind = 2 // 已合并（写入值与当前值相同）
	ResConflict     ResultKind = 3 // 冲突
	ResAhead        ResultKind = 4 // 基线超前
	ResKindMismatch ResultKind = 5 // 种类不符
	ResOverflow     ResultKind = 6 // 溢出
	ResDepFailed    ResultKind = 7 // 依赖失败
	ResGroupAborted ResultKind = 8 // 组内连带失败
)

func (r ResultKind) String() string {
	switch r {
	case ResApplied:
		return "applied"
	case ResMerged:
		return "merged"
	case ResConflict:
		return "conflict"
	case ResAhead:
		return "ahead"
	case ResKindMismatch:
		return "kind_mismatch"
	case ResOverflow:
		return "overflow"
	case ResDepFailed:
		return "dependency_failed"
	case ResGroupAborted:
		return "group_aborted"
	default:
		return "unknown"
	}
}

// Op is a single client operation inside a batch.
type Op struct {
	Seq     int64  // client-assigned operation sequence number, >= 1
	Type    OpType // OpSet or OpAdd
	Field   string // field name declared in the schema
	Value   int64  // Set: value to write
	BaseVer int64  // Set: base field version
	Delta   int64  // Add: non-zero delta
	Group   string // transaction group id, non-empty
	Depends bool   // group depends on its immediately preceding group
}

// FieldState is one field as returned by Get.
type FieldState struct {
	Kind    Kind
	Value   int64
	Version int64 // 0 iff an overwrite field is absent
	Present bool  // false only for an absent overwrite field
}

// DocSnapshot is the full document state returned by Get.
type DocSnapshot struct {
	DocID    string
	Schema   map[string]Kind
	Revision int64
	Fields   map[string]FieldState
}

// OpResult is the verdict for one operation.
type OpResult struct {
	Seq     int64
	Kind    ResultKind
	Value   int64 // conflict/ahead: current field value
	Version int64 // conflict/ahead: current field version
	Present bool  // whether Value is meaningful for an overwrite field
}

// BatchResult is the outcome of an accepted Sync call.
type BatchResult struct {
	Results []OpResult // aligned position-by-position with the input ops
}

// RejectError carries the first batch-level rejection reason.
type RejectError struct {
	Reason string
	Detail string
}

func (e *RejectError) Error() string {
	if e.Detail == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Detail
}

// Batch-level sentinel errors. Match with errors.Is on the wrapped
// RejectError (RejectError implements Is).
var (
	ErrInvalidParam  = errors.New("invalid parameter")
	ErrClockRollback = errors.New("clock rollback")
	ErrReplayExpired = errors.New("replay expired")
	ErrSeqGap        = errors.New("sequence gap")
	ErrUnknownDoc    = errors.New("unknown document")
)

// Is supports errors.Is(err, ErrClockRollback) and friends.
func (e *RejectError) Is(target error) bool {
	switch target {
	case ErrInvalidParam:
		return e.Reason == ErrInvalidParam.Error()
	case ErrClockRollback:
		return e.Reason == ErrClockRollback.Error()
	case ErrReplayExpired:
		return e.Reason == ErrReplayExpired.Error()
	case ErrSeqGap:
		return e.Reason == ErrSeqGap.Error()
	case ErrUnknownDoc:
		return e.Reason == ErrUnknownDoc.Error()
	default:
		return false
	}
}
