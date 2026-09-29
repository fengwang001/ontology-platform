package fulljoin

import "fmt"

// Side 标识变更来自左表还是右表。
type Side int

const (
	SideLeft Side = iota
	SideRight
)

func (s Side) String() string {
	switch s {
	case SideLeft:
		return "L"
	case SideRight:
		return "R"
	default:
		return "?"
	}
}

// Row 是输入侧的一行：按 Key 分组，ID 在该侧全局唯一。
type Row struct {
	Key string
	ID  string
	Val string
}

// Change 是一条变更日志输入：Kind 为 '+' 插入或 '-' 删除。
// 删除时以 Side+ID 定位行，Key/Val 仅用于日志展示。
type Change struct {
	Kind byte
	Side Side
	Row  Row
}

// OutRow 是全外连接物化视图中的一行。
// 配对行两侧均非 nil；补足行（补位行）缺侧为 nil。
type OutRow struct {
	Key   string
	Left  *Row
	Right *Row
}

// Entry 是变更日志中的一条输出条目，下游按序应用即可物化视图。
type Entry struct {
	Kind byte // '+' 输出一行，'-' 撤回一行
	Row  OutRow
}

func rowLabel(r *Row) string {
	if r == nil {
		return "∅"
	}
	return r.ID
}

// String 输出可读形态，如 (k: l1 ⋈ r1) 或 (k: l1 ⋈ ∅)。
func (o OutRow) String() string {
	return fmt.Sprintf("(%s: %s⋈%s)", o.Key, rowLabel(o.Left), rowLabel(o.Right))
}

// String 输出可读的变更，如 +L/l1。
func (c Change) String() string {
	return fmt.Sprintf("%c%s/%s", c.Kind, c.Side, c.Row.ID)
}

// String 输出可读的日志条目，如 -(k: ∅⋈r1)。
func (e Entry) String() string {
	return fmt.Sprintf("%c%s", e.Kind, e.Row)
}
