// Package table 把词法事件组装成表并提供流式 Parser。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// Table 是成功解析的结果；Header 即第一条记录。
type Table struct {
	Header  []cell.Cell
	Rows    [][]cell.Cell
	Records [][]cell.Cell // Header + Rows，便于逐字段比较
}

// Equal 逐字段比较值与引号标记。
func (t *Table) Equal(o *Table) bool {
	if len(t.Records) != len(o.Records) {
		return false
	}
	for i := range t.Records {
		a, b := t.Records[i], o.Records[i]
		if len(a) != len(b) {
			return false
		}
		for j := range a {
			if !a[j].Equal(b[j]) {
				return false
			}
		}
	}
	return true
}

// Builder 是 lexer.Sink：负责列数一致性、字段数/记录数上限。
type Builder struct {
	lim      lexer.Limits
	cur      []cell.Cell
	out      [][]cell.Cell
	recLimit bool
}

// NewBuilder 创建组装器。
func NewBuilder(lim lexer.Limits) *Builder {
	return &Builder{lim: lim, recLimit: lim.MaxRecords > 0}
}

// Field 实现 lexer.Sink。
func (b *Builder) Field(c cell.Cell) error {
	if b.lim.MaxFieldsRecord > 0 && len(b.cur) >= b.lim.MaxFieldsRecord {
		return lexer.ErrTooManyFields
	}
	b.cur = append(b.cur, c)
	return nil
}

// Record 实现 lexer.Sink；off 为换行字节绝对偏移。
func (b *Builder) Record(off int) error {
	if len(b.out) > 0 && len(b.cur) != len(b.out[0]) {
		return lexer.ErrArity
	}
	if b.recLimit && len(b.out) >= b.lim.MaxRecords {
		return lexer.ErrTooManyRecords
	}
	row := b.cur
	b.cur = nil
	b.out = append(b.out, row)
	return nil
}

// Finish 接收 EOF 隐式关闭的最后一条记录，产出表；空输入返回空表。
func (b *Builder) Finish() (*Table, error) {
	if b.cur != nil {
	if len(b.out) > 0 && len(b.cur) != len(b.out[0]) {
		return nil, lexer.ErrArity
		}
		if b.recLimit && len(b.out) >= b.lim.MaxRecords {
			return nil, lexer.ErrTooManyRecords
		}
		b.out = append(b.out, b.cur)
		b.cur = nil
	}
	t := &Table{Records: b.out}
	if len(b.out) > 0 {
		t.Header = b.out[0]
		t.Rows = b.out[1:]
	}
	return t, nil
}

// Parser 是可半包续传的流式解析器；单实例非并发安全。
type Parser struct {
	m    *lexer.Machine
	b    *Builder
	done bool
}

// NewParser 创建解析器。
func NewParser(lim lexer.Limits) *Parser {
	b := NewBuilder(lim)
	return &Parser{m: lexer.New(b, 0, lim), b: b}
}

// Feed 推入任意长度字节，可多次调用。
func (p *Parser) Feed(data []byte) error {
	if e := p.m.Err(); e != nil {
		return e
	}
	if p.done {
		return errors.New("parser closed")
	}
	return p.m.Feed(data)
}

// Close 结束流并返回表；终态后重复调用返回同一错误。
func (p *Parser) Close() (*Table, error) {
	if e := p.m.Err(); e != nil {
		return nil, e
	}
	p.done = true
	if e := p.m.EOF(); e != nil {
		return nil, e
	}
	return p.b.Finish()
}

// Count 返回状态机处理的字节总数。
func (p *Parser) Count() int { return p.m.Count() }

// Parse 一次性解析完整输入。
func Parse(data []byte, lim lexer.Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(data); err != nil {
		return nil, err
	}
	return p.Close()
}
