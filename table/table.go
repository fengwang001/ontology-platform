// Package table 把词法事件组装成记录与表，做列数一致性检查。
package table

import (
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/lexer"
)

// ErrFieldCount 是记录列数与第一条记录不一致。
var ErrFieldCount = errors.New("table: record field count mismatch")

// Table 是解析结果；Header 为 nil 表示无表头。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
	Width  int
}

// Error 携带字节偏移/记录号/字段号，包装词法或列数错误。
type Error struct {
	Kind   error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Kind }

// Config 控制表头、上限。
type Config struct {
	HasHeader     bool
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Collector 接收词法事件并组装表，实现 lexer.Sink。
type Collector struct {
	cfg    Config
	t      Table
	cur    []cell.Cell
	record int
	fatal  error
}

// NewCollector 构造事件收集器。
func NewCollector(cfg Config) *Collector { return &Collector{cfg: cfg} }

func (c *Collector) Field(cell cell.Cell) error {
	c.cur = append(c.cur, cell)
	return nil
}

func (c *Collector) EndRecord(off int) error {
	c.record++
	if c.t.Width == 0 {
		c.t.Width = len(c.cur)
		if c.cfg.HasHeader {
			c.t.Header = c.cur
			c.cur = nil
			return nil
		}
	} else if len(c.cur) != c.t.Width {
		return c.mkErr(ErrFieldCount, off, 1)
	}
	if !(c.cfg.HasHeader && c.record == 1) {
		c.t.Rows = append(c.t.Rows, c.cur)
	}
	c.cur = nil
	return nil
}

func (c *Collector) BlankLine() error { return nil }

func (c *Collector) Syntax(kind error, off, rec, fld int) error {
	return c.mkErr(kind, off, fld)
}

func (c *Collector) mkErr(kind error, off, fld int) error {
	c.fatal = &Error{Kind: kind, Offset: off, Record: c.record + 1, Field: fld}
	return c.fatal
}

// Err 返回组装过程中的终结错误。
func (c *Collector) Err() error { return c.fatal }

// Flush 在流结束时刷出无换行的最后一条记录。
func (c *Collector) Flush() error {
	if c.fatal != nil || (len(c.cur) == 0 && c.record == 0) {
		return c.fatal
	}
	return c.EndRecord(0)
}

// Table 返回已组装的表。
func (c *Collector) Table() *Table { return &c.t }

// Parser 是单次流式解析器；非并发安全。
type Parser struct {
	col *Collector
	lx  *lexer.Lexer
}

// New 构造流式解析器。
func New(cfg Config) *Parser {
	col := NewCollector(cfg)
	return &Parser{col: col, lx: lexer.New(col, lexer.Limits{
		MaxFieldBytes: cfg.MaxFieldBytes, MaxFields: cfg.MaxFields, MaxRecords: cfg.MaxRecords})}
}

// Feed 送入一段字节。
func (p *Parser) Feed(b []byte) error { return p.lx.Feed(b) }

// Close 结束流并返回表。
func (p *Parser) Close() (*Table, error) {
	err := p.lx.Close()
	return &p.col.t, err
}

// Parse 一次性解析。
func Parse(b []byte, cfg Config) (*Table, error) {
	p := New(cfg)
	if err := p.Feed(b); err != nil {
		return &p.col.t, err
	}
	return p.Close()
}
