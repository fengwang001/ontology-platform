package ontology

// Side 表示条目来自左表还是右表。
type Side string

const (
	SideLeft  Side = "left"
	SideRight Side = "right"
)

// Op 表示对某一张表的增量操作类型。
type Op string

const (
	OpInsert Op = "+"
	OpDelete Op = "-"
)

// Input 是一条输入变更：向指定侧插入或删除 (key, id) 行。
type Input struct {
	Side Side
	Op   Op
	Key  string
	ID   string
}

// JoinRow 是左外连接结果中的一行。
// 空填充行的 RightID == "" 且 RightPresent == false。
type JoinRow struct {
	Key          string
	LeftID       string
	RightID      string
	RightPresent bool
}

// Entry 是连接结果变更日志中的一条记录。
// Sign=+ 表示加入结果，Sign=- 表示从结果撤回。
type Entry struct {
	Sign   Op
	Row    JoinRow
	Reason string
}

// Logger 接收每一批输入的判定依据与产生的输出条目。
type Logger interface {
	LogBatch(inputs []Input, accepted bool, reason string, entries []Entry)
}

// Join 是增量左外连接组件。
type Join struct {
}

// NewJoin 创建连接组件，maxRows 为左右两表各自允许的最大行数。
func NewJoin(maxRows int) *Join {
	return nil
}

// Apply 原子地校验并应用一批同侧输入，返回本批产生的连接结果变更。
// 被拒绝时返回 *RejectError，且两表与日志均不发生任何变化。
func (j *Join) Apply(side Side, inputs []Input) ([]Entry, error) {
	return nil, nil
}

// Snapshot 返回某一时刻完整、自洽的左外连接视图。
func (j *Join) Snapshot() []JoinRow {
	return nil
}

// Log 返回已接受批次按顺序积累的全部输出条目。
func (j *Join) Log() []Entry {
	return nil
}

// SetLogger 安装判定日志记录器。
func (j *Join) SetLogger(l Logger) {
}
