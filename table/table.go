// Package table 把 lexer 事件组装成记录与表头。
package table

import (
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/lexer"
)

// Config 配置词法上限与总记录数上限（不含表头）；零值不限。
type Config struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Record 是一行字段，No 为记录号（从 1 起，表头不计）。
type Record struct {
	No    int
	Cells []cell.Cell
}

// Table 是解析结果：表头（可为 nil）与数据记录。
type Table struct {
	Header  []cell.Cell
	Records []Record
}

// ErrColumnCount 记录列数与首条记录不一致。
var ErrColumnCount = errors.New("table: record column count mismatch")

// ErrTooManyRecords 总记录数超限。
var ErrTooManyRecords = errors.New("table: too many records")

// ColumnError 携带列数错误的位置信息。
type ColumnError struct {
	Byte   int
	Record int
	Want   int
	Got    int
}

func (e *ColumnError) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d): want %d got %d", ErrColumnCount, e.Byte, e.Record, e.Want, e.Got)
}
func (e *ColumnError) Unwrap() error { return ErrColumnCount }

type sink struct {
	p *Parser
}

func (s *sink) Field(c cell.Cell) { s.p.cur = append(s.p.cur, c) }
func (s *sink) EndRecord(pos int) { s.p.endRecord(pos) }
func (s *sink) Fail(e *lexer.Error) {
	if s.p.err == nil {
		s.p.err = e
	}
}

// Parser 是增量解析器。单个实例非并发安全。
type Parser struct {
	m       *lexer.Machine
	sk      sink
	maxRec  int
	cur     []cell.Cell
	header  []cell.Cell
	records []Record
	width   int
	err     error
	closed  bool
	dataSeen bool
}

// NewParser 创建增量解析器。
func NewParser(cfg Config) *Parser {
	p := &Parser{maxRec: cfg.MaxRecords}
	p.m = lexer.NewMachine(lexer.Limits{MaxFieldBytes: cfg.MaxFieldBytes, MaxFields: cfg.MaxFields}, false, 0)
	p.sk.p = p
	return p
}

func (p *Parser) endRecord(pos int) {
	row := append([]cell.Cell(nil), p.cur...)
	if len(p.records) == 0 && p.width == 0 && !p.dataSeen {
		// 第一条记录作为表头
		p.header = row
		p.width = len(row)
		p.cur = p.cur[:0]
		p.dataSeen = true
		return
	}
	p.dataSeen = true
	if p.width > 0 && len(row) != p.width {
		if p.err == nil {
			p.err = &ColumnError{Byte: pos, Record: len(p.records) + 1, Want: p.width, Got: len(row)}
		}
		p.cur = p.cur[:0]
		return
	}
	rec := Record{No: len(p.records) + 1, Cells: row}
	if p.maxRec > 0 && rec.No > p.maxRec {
		if p.err == nil {
			p.err = &lexer.Error{Err: ErrTooManyRecords, Byte: pos, Record: rec.No, Field: 1}
		}
		p.cur = p.cur[:0]
		return
	}
	p.records = append(p.records, rec)
	p.cur = p.cur[:0]
}

// Feed 送入一段字节，可调用任意次。终态后返回同一错误。
func (p *Parser) Feed(b []byte) error {
	if p.err != nil {
		return p.err
	}
	if p.closed {
		return lexer.ErrTerminal
	}
	p.m.Run(b, &p.sk)
	return p.err
}

// Close 结束输入。
func (p *Parser) Close() error {
	if p.err != nil {
		return p.err
	}
	if p.closed {
		return lexer.ErrTerminal
	}
	p.closed = true
	p.m.Finish(&p.sk)
	return p.err
}

// Result 返回已组装的表（错误时为已产出的完整前缀）。
func (p *Parser) Result() *Table {
	return &Table{Header: p.header, Records: p.records}
}

// Bytes 返回状态机处理字节总数。
func (p *Parser) Bytes() int { return p.m.Bytes() }

// Parse 一次性解析。
func Parse(b []byte, cfg Config) (*Table, error) {
	p := NewParser(cfg)
	if err := p.Feed(b); err != nil {
		return p.Result(), err
	}
	err := p.Close()
	return p.Result(), err
}
