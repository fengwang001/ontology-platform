// Package table 把 lexer 的字段事件组装成记录表。
package table

import (
	"errors"
	"fmt"

	"ontology/cell"
	"ontology/lexer"
)

// ErrColumnMismatch 记录列数与首条记录不一致。
var ErrColumnMismatch = errors.New("record field count differs from first record")

// ErrTooManyRecords 总记录数超上限。
var ErrTooManyRecords = errors.New("record count exceeds max")

// Options 为解析资源上限，0 表示不限。
type Options struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Error 带字节偏移/记录/字段；Kind 为可判定哨兵。
type Error struct {
	Kind          error
	Offset        int
	Record, Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v: offset=%d record=%d field=%d", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Kind }

// Table 是解析结果。Header 为第一条记录。
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Parser 是流式解析器，包装 lexer.Lexer；非并发安全。
type Parser struct {
	opt   Options
	lx    *lexer.Lexer
	rec   []cell.Cell
	width int
	tab   *Table
	err   *Error
	seen  int // 已消费的 lexer 事件计数
}

func New(opt Options) *Parser {
	return &Parser{opt: opt, lx: lexer.New(lexer.Limits{MaxFieldBytes: opt.MaxFieldBytes, MaxFields: opt.MaxFields}),
		tab: &Table{}}
}

func wrapErr(e *lexer.Error) *Error {
	return &Error{Kind: e.Kind, Offset: e.Offset, Record: e.Record, Field: e.Field}
}

// Feed 送入一段字节。
func (p *Parser) Feed(b []byte) error {
	if p.err != nil {
		return p.err
	}
	if err := p.lx.Feed(b); err != nil {
		p.err = wrapErr(p.lx.Err())
		return p.err
	}
	return p.drain()
}

// Close 结束流并返回表或错误。
func (p *Parser) Close() (*Table, error) {
	if p.err != nil {
		return nil, p.err
	}
	if err := p.lx.Close(); err != nil {
		p.err = wrapErr(p.lx.Err())
		return nil, p.err
	}
	if err := p.drain(); err != nil {
		return nil, err
	}
	return p.tab, nil
}

func (p *Parser) drain() error {
	for _, e := range p.lx.Events()[p.seen:] {
		p.seen++
		p.rec = append(p.rec, e.C)
		if e.RecEnd {
			if len(p.tab.Header) == 0 {
				p.tab.Header = p.rec
				p.width = len(p.rec)
			} else {
				if len(p.rec) != p.width {
					p.err = &Error{Kind: ErrColumnMismatch, Offset: e.C.End, Record: e.Rec, Field: len(p.rec)}
					return p.err
				}
				p.tab.Rows = append(p.tab.Rows, p.rec)
			}
			p.rec = nil
			if p.opt.MaxRecords > 0 && len(p.tab.Header)+len(p.tab.Rows) > p.opt.MaxRecords {
				p.err = &Error{Kind: ErrTooManyRecords, Offset: e.C.End, Record: e.Rec, Field: e.Fl}
				return p.err
			}
		}
	}
	return nil
}

// Parse 一次性解析完整输入。
func Parse(b []byte, opt Options) (*Table, error) {
	p := New(opt)
	if err := p.Feed(b); err != nil {
		return nil, err
	}
	return p.Close()
}

// NBytes 返回底层状态机处理字节数。
func (p *Parser) NBytes() int { return p.lx.NBytes() }
