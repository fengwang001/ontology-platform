// Package table 把记录组装成表：列数一致性、表头、行列定位。
package table

import (
	"errors"

	"ontology/cell"
)

var (
	ErrColumnCount = errors.New("table: record column count mismatch")
	ErrTooManyCols = errors.New("table: record exceeds max fields")
	ErrTooManyRows = errors.New("table: too many records")
)

// Limits 上限配置；0 表示不限制。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Table 解析结果：Header 为第一条记录。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Parser 流式解析器；单实例非并发安全。
type Parser struct {
	lim Limits
}

func New(lim Limits) *Parser { return &Parser{lim: lim} }

func (p *Parser) Feed(b []byte) error { return nil }

func (p *Parser) Close() error { return nil }

func (p *Parser) Table() Table { return Table{} }

// Bytes 返回状态机处理过的字节总数。
func (p *Parser) Bytes() int { return 0 }

// Parse 一次性解析整个缓冲区。
func Parse(b []byte, lim Limits) (Table, error) { return Table{}, nil }
