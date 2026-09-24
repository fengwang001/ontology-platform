// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import "ontology/cell"

// Limits 为三类资源上限，0 表示不限制。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Pos 是错误位置：字节偏移从 0 起，记录号/字段号从 1 起。
type Pos struct {
	Offset int
	Record int
	Field  int
}

// Error 携带可判定哨兵错误与位置。
type Error struct {
	Err error
	Pos
}

func (e *Error) Error() string { return "" }
func (e *Error) Unwrap() error { return e.Err }

// Event 为字段/记录关闭事件。
type Event struct {
	Record bool
	Cell   cell.Cell
	Offset int
}

// Machine 是流式状态机（占位）。
type Machine struct{}

func New(_ Limits) *Machine { return &Machine{} }
