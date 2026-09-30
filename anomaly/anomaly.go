// Package anomaly 判定多版本事务历史中的隔离异常。
package anomaly

// Status 表示事务状态。
type Status string

const (
	Committed Status = "committed"
	Aborted   Status = "aborted"
)

// Version 标识某事务对某键的一次写所产生的版本。
type Version struct {
	Txn int
	Seq int
}

// Op 表示一个读或写操作。写键时 Read 必须为 nil；读键时 Read 指向所读版本。
type Op struct {
	Key  string
	Read *Version
}

// Txn 表示一个事务及其有序操作列表。
type Txn struct {
	ID     int
	Status Status
	Ops    []Op
}

// History 是一次判定的输入。Order 给出每个键的版本次序：
// 初始版本（Txn 为 0）最先，其后是各已提交事务对该键最后一次写的版本。
type History struct {
	Txns  []Txn
	Order map[string][]Version
}

// Category 是异常类别。
type Category string

const (
	None    Category = ""
	G0      Category = "G0"
	G1a     Category = "G1a"
	G1b     Category = "G1b"
	G1c     Category = "G1c"
	GSingle Category = "G-single"
	G2      Category = "G2"
)

// EdgeType 是依赖边类型。
type EdgeType uint8

const (
	WW EdgeType = 1 << iota // 写写边
	WR                      // 写读边
	RW                      // 读写边
)

// Edge 是事务依赖图中的一条边。
type Edge struct {
	From int
	To   int
	Type EdgeType
}

// Result 是判定结果。被拒绝时 Rejected 为 true，其余字段为零值。
type Result struct {
	Rejected bool
	ErrCode  string
	Err      error

	Category Category
	Level    string
	Witness  []int
	Edges    []Edge
	Reason   string
}

// Analyze 对给定历史进行隔离异常判定。它是无状态、确定性、可并发调用的。
func Analyze(h History) Result {
	return analyze(h)
}
