package leftjoin

// Side 标识变更来源：左表或右表。
type Side string

const (
	Left  Side = "left"
	Right Side = "right"
)

// Op 标识一条输入变更的操作类型。
type Op string

const (
	Insert Op = "+"
	Delete Op = "-"
)

// Change 是一条原子输入变更：向某一侧表插入或删除一行。
type Change struct {
	Side Side
	Op   Op
	Key  string
	ID   string
}

// Entry 是连接结果日志中的一条输出条目。
type Entry struct {
	Op      Op
	Key     string
	LeftID  string
	RightID string
	Reason  string
}

// RejectReason 描述一个批次被拒绝的可区分原因。
type RejectReason string

const (
	RejectEmptyKey      RejectReason = "empty key"
	RejectEmptyID       RejectReason = "empty id"
	RejectDuplicateID   RejectReason = "duplicate id in batch"
	RejectInsertExists  RejectReason = "insert of existing id"
	RejectDeleteMissing RejectReason = "delete of missing id"
	RejectTooManyRows   RejectReason = "total row count exceeds limit"
)

// RejectedBatchError 在输入批次非法时返回；批次不会产生任何副作用。
type RejectedBatchError struct {
	Reason RejectReason
	Index  int
	Change Change
}

func (e *RejectedBatchError) Error() string {
	return "leftjoin: batch rejected: " + string(e.Reason)
}

// Row 是左外连接结果中的一行。
type Row struct {
	Key     string
	LeftID  string
	RightID string
}

// Joiner 增量维护左外连接结果。
type Joiner struct {
	maxRows int
}

// New 创建 Joiner，maxRows 为左右两表允许的总行数上限。
func New(maxRows int) *Joiner {
	return &Joiner{maxRows: maxRows}
}

// Apply 原子地应用一个变更批次，返回该批次产生的有序输出条目。
func (j *Joiner) Apply(changes []Change) ([]Entry, error) {
	return nil, nil
}

// Snapshot 返回当前左外连接视图的逐行一致快照。
func (j *Joiner) Snapshot() []Row {
	return nil
}
