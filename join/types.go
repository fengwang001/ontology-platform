// Package join 实现一个增量左外连接（incremental left outer join）组件。
//
// 左表的每一行要么与右表中同键的所有行配对，要么在没有任何同键右行时
// 以一条“空填充行”（右侧为空）存在，两种形态互斥。左表或右表的插入、
// 删除都会被翻译成对连接结果的撤回（Delete）与插入（Insert）日志，
// 下游按日志顺序逐条应用即可始终得到正确的左外连接视图。
package join

// Side 标识操作作用于哪一侧表。
type Side int

const (
	Left Side = iota + 1
	Right
)

func (s Side) String() string {
	switch s {
	case Left:
		return "left"
	case Right:
		return "right"
	default:
		return "unknown"
	}
}

// Kind 标识操作或日志条目的种类。
type Kind int

const (
	Insert Kind = iota + 1
	Delete
)

func (k Kind) String() string {
	switch k {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	default:
		return "unknown"
	}
}

// Row 是左表或右表中的一行。
type Row struct {
	// ID 为行标识，同一侧表内必须唯一且非空。
	ID string
	// Key 为连接键，必须非空；两侧仅在 Key 相等时配对。
	Key string
	// Value 为业务负载，允许为空。
	Value string
}

// Op 表示对某一侧表的一次插入或删除。
type Op struct {
	Side Side
	Kind Kind
	Row  Row
}

// Entry 是连接结果变更日志（changelog）中的一条。
//
// LeftID/LeftValue 始终非空；RightID 为空串表示空填充行（无匹配右行）。
// Kind=Delete 表示撤回该结果行，Kind=Insert 表示加入该结果行。
type Entry struct {
	Kind       Kind
	Key        string
	LeftID     string
	LeftValue  string
	RightID    string
	RightValue string
}

// IsPadded 报告该条目是否为空填充行（右侧缺失）。
func (e Entry) IsPadded() bool { return e.RightID == "" }

// Result 是一次成功 Apply 的结果。
type Result struct {
	// Entries 为本批操作产生的、按确定性顺序排列的连接结果变更条目。
	Entries []Entry
}

// Reason 标识一次拒绝的可区分原因。
type Reason string

const (
	ReasonEmptyKey         Reason = "empty_key"
	ReasonEmptyID          Reason = "empty_id"
	ReasonUnknownSide      Reason = "unknown_side"
	ReasonUnknownKind      Reason = "unknown_kind"
	ReasonDuplicateID      Reason = "duplicate_id"
	ReasonIDNotFound       Reason = "id_not_found"
	ReasonRowLimitExceeded Reason = "row_limit_exceeded"
)

// Decision 是判定日志中的一条记录：输入操作、产生的输出条目与判定依据。
type Decision struct {
	// Index 为本批操作中的序号（从 0 开始）。
	Index int
	Op    Op
	// Accepted 为 true 表示接受并落库；false 表示整批被拒绝。
	Accepted bool
	// Reason 在被拒绝时给出可区分原因；接受时为空。
	Reason Reason
	// Detail 为人类可读的判定依据。
	Detail string
	// Entries 为该操作产生的连接结果变更条目（仅接受时有值）。
	Entries []Entry
}
