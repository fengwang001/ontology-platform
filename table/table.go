// Package table 把 lexer 事件组装成记录与表。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// ErrColumnCount 表示记录字段数与第一条记录不一致。
var ErrColumnCount = errors.New("wrong number of fields in record")

// Table 是成功解析的结果；Header 为第一条记录（可能为空表 nil）。
type Table struct {
	Records [][]cell.Cell
}

// Header 返回第一条记录。
func (t *Table) Header() []cell.Cell { return nil }

// Parser 是流式表组装器；单个实例不要求并发安全。
type Parser struct{}

// New 创建流式解析器。
func New(lim lexer.Limits) *Parser { return &Parser{} }

// Feed 送入一段字节。
func (p *Parser) Feed(b []byte) error { return nil }

// Close 结束输入并返回已组装的表。
func (p *Parser) Close() (*Table, error) { return &Table{}, nil }

// Builder 供 par 重放事件使用：带全局基址的事件组装器。
type Builder struct{}

// NewBuilder 创建事件组装器。
func NewBuilder(lim lexer.Limits) *Builder { return &Builder{} }

// Add 重放一个事件。
func (b *Builder) Add(e lexer.Event) error { return nil }

// Table 返回组装结果。
func (b *Builder) Table() *Table { return &Table{} }
