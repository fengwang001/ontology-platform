package filterview

import "errors"

// 哨兵错误：拒绝原因彼此可区分，调用方使用 errors.Is 判定。
var (
	// ErrInvalidArgument 参数非法（nil 视图配置、区间颠倒、nil 行等）。
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrEmptyKey 行标识（主键）为空。
	ErrEmptyKey = errors.New("empty key")
	// ErrKeyMismatch 更新操作的前像与后像主键不同。
ErrKeyMismatch = errors.New("update before/after key mismatch")
	// ErrDuplicateKey 插入时主键在源表中已存在。
	ErrDuplicateKey = errors.New("duplicate key")
	// ErrKeyNotFound 删除或更新时主键在源表中不存在。
	ErrKeyNotFound = errors.New("key not found")
	// ErrPreimageMismatch 更新或删除提供的前像与源表当前行不一致。
	ErrPreimageMismatch = errors.New("preimage does not match current row")
)

// Kind 标识一条变更操作的类型。
type Kind int

const (
	KindUnknown Kind = iota
	KindInsert
	KindDelete
	KindUpdate
)

func (k Kind) String() string {
	switch k {
	case KindInsert:
		return "insert"
	case KindDelete:
		return "delete"
	case KindUpdate:
		return "update"
	default:
		return "unknown"
	}
}

// Row 是源表中的一行。Key 为字符串主键，Value 为被过滤列上的整数值。
type Row struct {
	Key   string
	Value int
}

// Op 是一条源表变更。
//   - 插入：After 为待插入行，Before 留空。
//   - 删除：Before 为调用方持有的前像（须与源表当前行一致），After 留空。
//   - 更新：Before 为前像，After 为后像，二者 Key 必须相同。
type Op struct {
	Kind   Kind
	Before Row
	After  Row
}

// ChangeKind 标识一条视图净变化。
type ChangeKind int

const (
	ChangeUnknown ChangeKind = iota
	// ChangeRetract 撤回：把旧行从视图中移除。
	ChangeRetract
	// ChangeAdd 写入：把新行加入视图。
	ChangeAdd
)

func (c ChangeKind) String() string {
	switch c {
	case ChangeRetract:
		return "retract"
	case ChangeAdd:
		return "add"
	default:
		return "unknown"
	}
}

// Change 是视图的一条净变化。
type Change struct {
	Kind ChangeKind
	Row  Row
}

// LogEntry 是一条判定日志：记录输入操作、输出净变化与判定依据。
// 同一下标的输入在每次运行中产生完全相同的日志内容。
type LogEntry struct {
	// Seq 是该操作在全部已成功提交批次中的全局序号（从 0 开始）。
	Seq int
	// Index 是操作在其所属批处理中的下标。
	Index int
	// Input 是输入操作的可复现文本形式。
	Input string
	// MatchedBefore / MatchedAfter 表示前像/后像是否落在过滤区间内。
	MatchedBefore bool
	MatchedAfter  bool
	// Reason 是判定依据的人类可读说明。
	Reason string
	// Output 是该操作产生的视图净变化（先撤回后写入的顺序）。
	Output []Change
}

// View 是源表上满足左闭右开区间 [Low, High) 的行级过滤视图。
// 所有方法均可被多个 goroutine 并发调用；读取得到的是逐字段一致的快照。
type View struct {
	// 骨架阶段为空实现。
}

// New 创建过滤视图。low > high 时返回 ErrInvalidArgument；
// low == high 是合法的空区间（没有任何行满足条件）。
func New(low, high int) (*View, error) {
	return nil, nil
}

// Apply 原子地应用一批变更，返回每一下标对应的视图净变化。
// 批内任意操作非法时整批拒绝：源表、视图、已提交日志均不变，
// 返回 *BatchError 说明全部原因。
func (v *View) Apply(ops []Op) ([][]Change, error) {
	return nil, nil
}

// SourceSnapshot 返回源表当前内容的一致快照，顺序按键升序。
func (v *View) SourceSnapshot() []Row {
	return nil
}

// ViewSnapshot 返回当前过滤视图内容的一致快照，顺序按键升序。
func (v *View) ViewSnapshot() []Row {
	return nil
}

// Log 返回自创建以来全部已成功提交操作的判定日志快照，按序号升序。
func (v *View) Log() []LogEntry {
	return nil
}
