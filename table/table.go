// Package table 把状态机事件组装成记录表。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Table 是成功解析出的记录集合。
type Table struct {
	Records [][]cell.Cell
	Header  []cell.Cell
}

// Parser 是支持半包续传的流式解析器；单实例非并发安全。
type Parser struct {
	m   *lexer.Machine
	tab *Table
	err error
}

// Options 配置上限。
type Options = lexer.Limits

// New 创建流式解析器。
func New(opt Options) *Parser { return nil }

// Feed 追加任意长度的输入片段。
func (p *Parser) Feed(b []byte) error { return nil }

// Close 结束输入。
func (p *Parser) Close() error { return nil }

// Table 返回已产出的表（含可能保留的前缀）。
func (p *Parser) Table() *Table { return nil }

// Parse 一次性解析完整输入。
func Parse(b []byte, opt Options) (*Table, error) { return nil, nil }

// BytesSeen 返回状态机处理的字节总次数。
func (p *Parser) BytesSeen() int { return 0 }
