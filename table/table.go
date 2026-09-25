// Package table 把 lexer 的字段事件组装成记录表。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// ErrColumnCount 是列数与首条记录不一致错误。
var ErrColumnCount = errors.New("column count mismatch")

// Table 是解析结果。
type Table struct {
	Records [][]cell.Cell
	Header  []cell.Cell
	HasHeader bool
}

// Options 配置上限与是否把首行视为表头。
type Options struct {
	Limits    cell.Limits
	HasHeader bool
}

// Parser 是流式组装器。单个实例不要求并发安全。
type Parser struct {
	opt    Options
	col    int
	cur  []cell.Cell
	tab  Table
	lx   *lexer.Lexer
	seen bool
	line int
}

// NewParser 创建流式解析器。
func NewParser(opt Options) *Parser {
	p := &Parser{opt: opt}
	p.lx = lexer.New(lexer.Config{Limits: opt.Limits, EnforceLimits: true}, p)
	return p
}

func (p *Parser) Field(c cell.Cell) { p.cur = append(p.cur, c) }
func (p *Parser) SkipLine()         { p.line++ }

func (p *Parser) Record() {
	n := len(p.cur)
	if p.col == 0 {
		p.col = n
	} else if n != p.col {
		pos := 0
		if n > 0 {
			pos = p.cur[n-1].End
		}
		p.lx.FailFromHandler(&lexer.Error{Err: ErrColumnCount, Offset: pos, Record: p.line + 1, Field: n})
		return
	}
	rec := make([]cell.Cell, n)
	copy(rec, p.cur)
	p.cur = p.cur[:0]
	if p.opt.HasHeader && !p.seen {
		p.tab.Header = rec
		p.tab.HasHeader = true
	} else {
		p.tab.Records = append(p.tab.Records, rec)
	}
	p.seen = true
	p.line++
}

// Feed 喂入数据。
func (p *Parser) Feed(b []byte) error { return p.lx.Feed(b) }

// Close 结束并返回表。
func (p *Parser) Close() (*Table, error) {
	if err := p.lx.Close(); err != nil {
		return nil, err
	}
	return &p.tab, nil
}

// Parse 一次性解析。
func Parse(b []byte, opt Options) (*Table, error) {
	p := NewParser(opt)
	if err := p.Feed(b); err != nil {
		return nil, err
	}
	return p.Close()
}

// Assemble 对已切分拼好的记录做列数一致性校验，返回新切片。
func Assemble(records [][]cell.Cell) ([][]cell.Cell, error) {
	out := make([][]cell.Cell, 0, len(records))
	cols := 0
	for ri, rec := range records {
		if cols == 0 {
			cols = len(rec)
		} else if len(rec) != cols {
			pos := 0
			if len(rec) > 0 {
				pos = rec[len(rec)-1].End
			}
			return nil, &lexer.Error{Err: ErrColumnCount, Offset: pos, Record: ri + 1, Field: len(rec)}
		}
		out = append(out, append([]cell.Cell(nil), rec...))
	}
	return out, nil
}
