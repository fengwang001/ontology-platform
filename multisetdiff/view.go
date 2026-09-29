package multisetdiff

// Side 标识变更来自左侧还是右侧多重集。
type Side int

const (
	Left Side = iota + 1
	Right
)

// Change 是一行的多重集重数增量：Delta>0 插入若干份，Delta<0 删除若干份。
type Change struct {
	Side  Side
	Key   string
	Delta int64
}

// RejectReason 是整批提交被拒绝的可区分原因。
type RejectReason string

const (
	ReasonEmptyBatch    RejectReason = "empty_batch"
	ReasonZeroDelta     RejectReason = "zero_delta"
	ReasonInvalidSide   RejectReason = "invalid_side"
	ReasonUnderflow     RejectReason = "underflow"
	ReasonLimitExceeded RejectReason = "limit_exceeded"
)

// RejectError 描述一次被整体拒绝的提交及其确定性原因。
type RejectError struct {
	Reason RejectReason
	Index  int
	Key    string
}

func (e *RejectError) Error() string { return "" }

// LogEntry 是结果视图输出的一条变更日志。
type LogEntry struct {
	Key     string
	OldMult int64
	NewMult int64
}

// Delta 为本条日志携带的重数增量。
func (e LogEntry) Delta() int64 { return 0 }

// Decision 记录一次提交的输入、判定依据与输出，用于可复现审计。
type Decision struct {
	Accepted bool
	Reject   *RejectError
	Changes  []Change
	Outputs  []LogEntry
}

// View 是多重集差 L \ R 的增量物化视图。
type View struct{}

// DefaultMaxDistinctRows 是每个视图允许同时存在的不同行数上限。
const DefaultMaxDistinctRows = 1 << 20

// NewView 创建空视图。
func NewView(maxDistinctRows int) *View { return &View{} }

// Snapshot 是某一时刻视图的不可变只读快照。
type Snapshot map[string]int64

// Commit 原子校验并应用一批左右两侧变更，成功时输出变更日志。
func (v *View) Commit(changes []Change) ([]LogEntry, error) { return nil, nil }

// Snapshot 复制当前视图；不阻塞且可与 Commit 并发。
func (v *View) View() Snapshot { return nil }

// Mult 返回指定行的当前结果重数（不存在即为 0）。
func (v *View) Mult(key string) int64 { return 0 }

// SelfCheck 校验内部不变量并对照独立批量重算，返回不一致错误。
func (v *View) SelfCheck() error { return nil }

// BatchRecompute 忽略已有视图，由两侧当前重数忽略已有视图，由两侧当前重数独立批量重算结果。
func BatchRecompute(left, right map[string]int64) map[string]int64 { return nil }

// Log 返回复用视图启动以来已提交的全部变更日志。
func (v *View) Log() []LogEntry { return nil }

// Decisions 返回复用视图启动以来每一次提交（含被拒绝）的判定记录。
func (v *View) Decisions() []Decision { return nil }
