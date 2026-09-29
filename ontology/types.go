package ontology

import "errors"

// Presence 表示列值的三态：缺席、显式空值、字符串。
type Presence int

const (
	Absent Presence = iota
	Null
	String
)

// ColumnValue 是列值的三态表示。
// Presence == String 时 Text 为实际字符串；空串只能由 Presence == String 表达。
type ColumnValue struct {
	Presence Presence
	Text     string
}

// AbsentValue 构造一个缺席值。
func AbsentValue() ColumnValue { return ColumnValue{Presence: Absent} }

// NullValue 构造一个显式空值。
func NullValue() ColumnValue { return ColumnValue{Presence: Null} }

// StringValue 构造一个字符串值（允许空串）。
func StringValue(s string) ColumnValue { return ColumnValue{Presence: String, Text: s} }

// Event 是批内的一条变更事件。
type Event struct {
	// IsInsert 为 true 表示插入，false 表示部分列更新。
	IsInsert bool
	Key      string
	// Values 为列名到三态列值的映射。
	// 插入：必须给出全部列（值可以是 Null / String，不允许 Absent）。
	// 更新：只含本次变更列；Before 给出这些列变更前的镜像。
	Values map[string]ColumnValue
	// Before 仅更新事件使用，键必须与 Values 完全相同。
	Before map[string]ColumnValue
}

// Outcome 是同一主键在批内合并后的至多一条输出。
type Outcome struct {
	IsInsert bool
	Key      string
	// 插入：插入后的全行；更新：剔除无变化列后的变更列。
	Values map[string]ColumnValue
	// 仅更新：变更列在批内首次被更新时的镜像值。
	Before map[string]ColumnValue
}

// ReasonCode 标识整批被拒绝的互斥原因类别。
type ReasonCode string

const (
	ReasonEmptyKey             ReasonCode = "empty_key"
	ReasonUnknownColumn        ReasonCode = "unknown_column"
	ReasonAbsentColumnValue    ReasonCode = "absent_column_value"
	ReasonInsertMissingColumn  ReasonCode = "insert_missing_column"
	ReasonKeyNotFound          ReasonCode = "key_not_found"
	ReasonKeyAlreadyExists     ReasonCode = "key_already_exists"
	ReasonBeforeColumnMismatch ReasonCode = "before_column_mismatch"
	ReasonBeforeValueMismatch  ReasonCode = "before_value_mismatch"
)

// BatchError 描述整批拒绝的原因；不同类别由 Reason 区分，互不重叠。
type BatchError struct {
	Reason ReasonCode
	// EventIndex 为批内触发拒绝的事件下标。
	EventIndex int
	Column     string
	Message    string
}

func (e *BatchError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Reason) + ": " + e.Message
}

// ErrEmptyBatch 表示空批次（不构成拒绝，也不产生输出）。
var ErrEmptyBatch = errors.New("ontology: empty batch")

func newBatchError(reason ReasonCode, idx int, column, msg string) *BatchError {
	return &BatchError{Reason: reason, EventIndex: idx, Column: column, Message: msg}
}

func sameValue(a, b ColumnValue) bool {
	if a.Presence != b.Presence {
		return false
	}
	return a.Presence != String || a.Text == b.Text
}
