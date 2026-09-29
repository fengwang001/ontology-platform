package antijoin

// Side 标识变更来自哪一侧。
type Side int

const (
	Left Side = iota
	Right
)

// Op 标识行变更的操作种类。
type Op int

const (
	Insert Op = iota
	Delete
)

// Row 是一侧输入中的一行。
//
// ID 是行标识，在同一侧内唯一且不能为空。Key 为连接键；KeyNil 为 true
// 表示该键是 SQL 空值：空值不与任何值相等（包括另一个空值）。
type Row struct {
	ID     string
	Key    string
	KeyNil bool
}

// Change 是一条行变更。
type Change struct {
	Side Side
	Op   Op
	Row  Row
}

// EventOp 是结果变更日志事件的种类。
type EventOp int

const (
	Enter EventOp = iota
	Leave
)

// Event 是结果变更日志中的一条事件。
type Event struct {
	Seq    int64
	Op     EventOp
	LeftID string
}

// View 是反连接（anti-join）的增量物化视图。
type View struct {
}

// DefaultMaxRows 是每一侧允许的最大行数。
const DefaultMaxRows = 0

// New 创建一个使用默认行数上限的视图。
func New() *View {
	return nil
}

// Apply 原子地校验并应用一批两侧行变更，返回该批产生的结果变更事件。
//
// 任一变更非法时整批拒绝，错误原因互不相同，且视图与日志保持不变。
func (v *View) Apply(changes []Change) ([]Event, error) {
	return nil, nil
}

// Snapshot 返回当前结果成员（左侧行 ID），顺序确定。
func (v *View) Snapshot() []string {
	return nil
}

// Log 返回截至目前输出的全部变更日志。
func (v *View) Log() []Event {
	return nil
}

// SelfCheck 将当前增量状态与批量重算结果比对，不一致时返回错误。
func (v *View) SelfCheck() error {
	return nil
}

// BatchCompute 依据给定的左右两侧行集合批量重算反连接结果。
func BatchCompute(left, right []Row) []string {
	return nil
}
