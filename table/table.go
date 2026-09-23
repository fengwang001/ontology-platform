// Package table 把词法记录组装成表。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Table 是解析结果：按记录存放的字段。
type Table struct {
	Records [][]cell.Cell
}

// Parser 流式组装器，包装 lexer。非并发安全。
type Parser struct {
	tab Table
}

// New 创建解析器。
func New(_ lexer.Limits) *Parser { return &Parser{} }

// Table 返回已解析表。
func (p *Parser) Table() *Table { return &p.tab }

// Feed / Close 委托给内部 lexer（实现见后）。
func (p *Parser) Feed(b []byte) error { return nil }
func (p *Parser) Close() error        { return nil }
