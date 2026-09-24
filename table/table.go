// Package table 把词法记录组装成表，并检查列数一致性与记录数上限。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// Config 是全部可配置上限；0 表示不限制。
type Config struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

var (
	ErrColumnMismatch = errors.New("record field count differs from first record")
	ErrTooManyRecords = errors.New("record count exceeds max records")
)

// Error 是表级错误，携带出错位置。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Table 是解析出的表。
type Table struct {
	Recs [][]cell.Cell
}

// Records 返回全部记录（含第一条，即表头）。
func (t *Table) Records() [][]cell.Cell { return t.Recs }

// NumRecords 返回记录数。
func (t *Table) NumRecords() int { return len(t.Recs) }

// NumCols 返回列数（无记录时为 0）。
func (t *Table) NumCols() int {
	if len(t.Recs) == 0 {
		return 0
	}
	return len(t.Recs[0])
}

// Header 返回第一条记录（表头）；无记录时返回 nil。
func (t *Table) Header() []cell.Cell {
	if len(t.Recs) == 0 {
		return nil
	}
	return t.Recs[0]
}

// At 按 1 起的记录号、字段号定位字段。
func (t *Table) At(record, field int) cell.Cell { return t.Recs[record-1][field-1] }

// Parser 是流式表解析器；非并发安全。
type Parser struct {
	cfg     Config
	lx      *lexer.Lexer
	t       *Table
	cur     []cell.Cell
	cols    int
	colsSet bool
	recNo   int
	err     error
}

// NewParser 创建解析器。
func NewParser(cfg Config) *Parser {
	p := &Parser{cfg: cfg, t: &Table{}}
	p.lx = lexer.New(lexer.Config{MaxFieldBytes: cfg.MaxFieldBytes, MaxFields: cfg.MaxFields}, p)
	p.lx.SetAbort(func() bool { return p.err != nil })
	return p
}

// Field 实现 lexer.Handler。
func (p *Parser) Field(c cell.Cell) {
	if p.err == nil {
		p.cur = append(p.cur, c)
	}
}

// Record 实现 lexer.Handler。
func (p *Parser) Record() {
	if p.err != nil {
		return
	}
	p.recNo++
	off := 0
	if n := len(p.cur); n > 0 {
		off = p.cur[n-1].End
	}
	if p.cfg.MaxRecords > 0 && p.recNo > p.cfg.MaxRecords {
		p.err = &Error{Err: ErrTooManyRecords, Offset: off, Record: p.recNo, Field: len(p.cur)}
		return
	}
	if !p.colsSet {
		p.cols, p.colsSet = len(p.cur), true
	} else if len(p.cur) != p.cols {
		p.err = &Error{Err: ErrColumnMismatch, Offset: off, Record: p.recNo, Field: len(p.cur)}
		return
	}
	p.t.Recs = append(p.t.Recs, p.cur)
	p.cur = nil
}

// Error 实现 lexer.Handler（词法错误由 Lexer 自身保存并返回）。
func (p *Parser) Error(e *lexer.Error) {}

// Feed 送入任意长度的字节块。
func (p *Parser) Feed(b []byte) error {
	if p.err != nil {
		return p.err
	}
	err := p.lx.Feed(b)
	if p.err != nil {
		return p.err
	}
	return err
}

// Close 结束解析并返回表；出错时返回已产出的部分表与错误。
func (p *Parser) Close() (*Table, error) {
	if p.err != nil {
		return p.t, p.err
	}
	err := p.lx.Close()
	if p.err != nil {
		return p.t, p.err
	}
	if err != nil {
		return p.t, err
	}
	return p.t, nil
}

// Table 返回目前已组装的（可能部分的）表。
func (p *Parser) Table() *Table { return p.t }

// Err 返回终态错误（若有）。
func (p *Parser) Err() error { return p.err }

// Processed 返回状态机处理过的字节总数。
func (p *Parser) Processed() int { return p.lx.Processed() }

// Parse 一次性解析完整输入。
func Parse(b []byte, cfg Config) (*Table, error) {
	p := NewParser(cfg)
	if err := p.Feed(b); err != nil {
		return p.Table(), err
	}
	return p.Close()
}
