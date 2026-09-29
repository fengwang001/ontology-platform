// Package cdc 提供变更捕获场景下的模式演进列映射能力：
// 将旧版本事件按列名投影到当前（目标）模式，并做可判定的类型转换。
package cdc

import "fmt"

// ColumnType 列类型，仅支持整数与字符串。
type ColumnType int

const (
	TypeInt ColumnType = iota
	TypeString
)

// String 返回列类型的可读名称。
func (t ColumnType) String() string {
	switch t {
	case TypeInt:
		return "int"
	case TypeString:
		return "string"
	default:
		return fmt.Sprintf("ColumnType(%d)", int(t))
	}
}

// Valid 判定列类型是否合法。
func (t ColumnType) Valid() bool {
	return t == TypeInt || t == TypeString
}

// Zero 返回该类型的零值：int 为 int64(0)，string 为 ""。
func (t ColumnType) Zero() any {
	if t == TypeInt {
		return int64(0)
	}
	return ""
}

// Column 模式中的一列。
type Column struct {
	Name     string
	Type     ColumnType
	Required bool
}

// Schema 某一完整版本的不可变模式快照。
type Schema struct {
	Version int
	Columns []Column
}

// Event 一条待投影的旧版本事件，Fields 的值仅允许 int/int64/string。
type Event struct {
	Version int
	Fields  map[string]any
}
