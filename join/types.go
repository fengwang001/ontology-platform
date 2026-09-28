package join

import "fmt"

// Row 标识多重集表中的一行：等值连接键 Key + 负载值 Value。
// 空字符串代表 SQL NULL，在输入校验阶段即被拒绝。
type Row struct {
	Key   string
	Value string
}

func (r Row) String() string {
	return fmt.Sprintf("(key=%s, value=%s)", quote(r.Key), quote(r.Value))
}

// RowChange 表示对一行重数的增量变更：Delta>0 为插入，Delta<0 为删除。
type RowChange struct {
	Key   string
	Value string
	Delta int64
}

func (c RowChange) Row() Row { return Row{Key: c.Key, Value: c.Value} }

func (c RowChange) String() string {
	return fmt.Sprintf("(key=%s, value=%s, delta=%d)", quote(c.Key), quote(c.Value), c.Delta)
}

// Batch 是一次原子提交：对左、右两张表的变更必须同时生效或同时不生效。
type Batch struct {
	Left  []RowChange
	Right []RowChange
}

// JoinTuple 是连接结果中的一行（不含重数）：连接键与两侧负载值。
type JoinTuple struct {
	Key        string
	LeftValue  string
	RightValue string
}

func (t JoinTuple) String() string {
	return fmt.Sprintf("(key=%s, left=%s, right=%s)", quote(t.Key), quote(t.LeftValue), quote(t.RightValue))
}

// less 按 (Key, LeftValue, RightValue) 字典序比较，用于输出差分的稳定有序排列。
func (t JoinTuple) less(o JoinTuple) bool {
	if t.Key != o.Key {
		return t.Key < o.Key
	}
	if t.LeftValue != o.LeftValue {
		return t.LeftValue < o.LeftValue
	}
	return t.RightValue < o.RightValue
}

// JoinDelta 是一条带符号的结果差分：Delta>0 新增重数，Delta<0 撤销重数。
// 下游按顺序把差分累加到上一批的结果上，始终得到当前批的完整连接结果。
type JoinDelta struct {
	JoinTuple
	Delta int64
}

func (d JoinDelta) String() string {
	return fmt.Sprintf("%s delta=%d", d.JoinTuple.String(), d.Delta)
}

// BatchResult 是一次 Apply 的判定结果。
type BatchResult struct {
	Accepted bool
	// Deltas 仅在 Accepted 时非空可能非空，按 (Key,LeftValue,RightValue) 有序，
	// 且只包含差分重数非零的元组。
	Deltas []JoinDelta
	// Err 仅在拒绝时非空。
	Err *RejectError
}

// formatChanges 把一批变更格式化为稳定字符串，供日志使用。
func formatChanges(changes []RowChange) string {
	if len(changes) == 0 {
		return "[]"
	}
	s := "["
	for i, c := range changes {
		if i > 0 {
			s += ", "
		}
		s += c.String()
	}
	return s + "]"
}
