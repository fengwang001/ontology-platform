package merger

import "fmt"

// ColumnValue 严格三态表达一个列值：缺席、显式空值、字符串。
type ColumnValue struct {
	present bool
	null    bool
	str     string
}

// Absent 返回一个“缺席”列值（该列不在本次事件中）。
func Absent() ColumnValue { return ColumnValue{} }

// Null 返回一个“显式空值”列值。
func Null() ColumnValue { return ColumnValue{present: true, null: true} }

// String 返回一个字符串列值；空串与显式空值不同。
func String(s string) ColumnValue { return ColumnValue{present: true, str: s} }

// Present 报告该列是否在事件中出现（显式空值与字符串都为 true）。
func (v ColumnValue) Present() bool { return v.present }

// IsNull 报告该列是否为显式空值。
func (v ColumnValue) IsNull() bool { return v.present && v.null }

// IsString 报告该列是否为字符串值（含空串）。
func (v ColumnValue) IsString() bool { return v.present && !v.null }

// StringValue 返回底层字符串；非字符串值时第二个返回值为 false。
func (v ColumnValue) StringValue() (string, bool) {
	if v.present && !v.null {
		return v.str, true
	}
	return "", false
}

// Equal 按三态语义比较两个列值：缺席、显式空值、空串互不相等。
func (v ColumnValue) Equal(o ColumnValue) bool {
	if v.present != o.present {
		return false
	}
	if !v.present {
		return true
	}
	if v.null != o.null {
		return false
	}
	return v.str == o.str
}

// String 返回列值的可读表示，用于日志与自检：
// 缺席为 <absent>、显式空值为 <null>、字符串为带引号形式。
func (v ColumnValue) String() string {
	switch {
	case !v.present:
		return "<absent>"
	case v.null:
		return "<null>"
	default:
		return fmt.Sprintf("%q", v.str)
	}
}
