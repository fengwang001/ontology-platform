package ontology

// ChangeKind 表示一条变更的符号：插入或撤回。
type ChangeKind int

const (
	// KindInsert 插入，净多重性 +1。
	KindInsert ChangeKind = +1
	// KindRetract 撤回，净多重性 -1。
	KindRetract ChangeKind = -1
)

// Change 是变更流中的一条条目。
type Change struct {
	Group string
	Value string
	Kind  ChangeKind
}

// RejectReason 是批次被拒绝的可区分原因。
type RejectReason string

const (
	// ReasonNone 未被拒绝。
	ReasonNone RejectReason = ""
	// ReasonTooManyEntries 条目数超过上限。
	ReasonTooManyEntries RejectReason = "too_many_entries"
	// ReasonEmptyGroup 组名为空。
	ReasonEmptyGroup RejectReason = "empty_group"
	// ReasonEmptyValue 值为空。
	ReasonEmptyValue RejectReason = "empty_value"
	// ReasonInvalidKind 变更符号非法（既非插入也非撤回）。
	ReasonInvalidKind RejectReason = "invalid_kind"
	// ReasonRetractZero 撤回一个当前净多重性为零的值。
	ReasonRetractZero RejectReason = "retract_zero"
)

// GroupDelta 是单个组在一批前后的去重计数变化。
type GroupDelta struct {
	Group  string
	Before int
	After  int
	Delta  int
}

// ApplyResult 是一批变更的判定结果。
type ApplyResult struct {
	Accepted   bool
	Reason     RejectReason
	EntryIndex int
	Changes    []GroupDelta
}
