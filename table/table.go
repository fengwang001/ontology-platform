// Package table 把 lexer 事件组装成表，负责列数一致性。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Table 是解析成功的结果。Header 指向 Rows[0]（无表头语义校验）。
type Table struct {
	Rows   [][]cell.Cell
	Header []cell.Cell
	Width  int
}

// Assembler 实现 lexer.Sink，收集记录并校验列数。
type Assembler struct {
	tab     Table
	cur     []cell.Cell
	lastEnd int
}

func (a *Assembler) Emit(c cell.Cell) { a.cur = append(a.cur, c) }

// EndRecord 对「与第一条记录列数不一致」立即返回 lexer.ErrColumnCount，
// 位置（偏移/记录号/字段号）由 machine 按当前位置填充。
func (a *Assembler) EndRecord() error {
	row := append([]cell.Cell(nil), a.cur...)
	a.cur = a.cur[:0]
	if len(row) == 0 {
		return nil
	}
	if a.tab.Width == 0 {
		a.tab.Width = len(row)
	} else if len(row) != a.tab.Width {
		return lexer.ErrColumnCount
	}
	a.tab.Rows = append(a.tab.Rows, row)
	a.tab.Header = a.tab.Rows[0]
	return nil
}

// Table 返回已组装的表。
func (a *Assembler) Table() *Table { return &a.tab }

// Parse 一次性解析整个输入。
func Parse(cfg lexer.Config, data []byte) (*Table, error) {
	a := &Assembler{}
	m := lexer.NewMachine(cfg, a)
	if err := m.Feed(data); err != nil {
		return a.Table(), err
	}
	if err := m.Close(); err != nil {
		return a.Table(), err
	}
	return a.Table(), nil
}
