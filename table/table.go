package table

import "ontology/cell"

// Limits 是解析上限，0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Table 是解析结果。
type Table struct {
	Rows [][]cell.Cell
}

// Parse 一次性解析。
func Parse(p []byte) (*Table, error) { return nil, nil }

// Parser 是流式解析器（单实例非并发安全）。
type Parser struct{}

// NewParser 创建流式解析器。
func NewParser(lim Limits) *Parser { return &Parser{} }

// Feed 喂入字节。
func (p *Parser) Feed(b []byte) error { return nil }

// Close 结束。
func (p *Parser) Close() (*Table, error) { return nil, nil }
