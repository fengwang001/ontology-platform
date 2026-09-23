// Package table 把 lexer 事件组装成记录与表头，校验列数并定位错误。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Record 是一行字段。
type Record = []cell.Cell

// Table 是解析结果；Header 为第一条记录，Records 含全部记录。
type Table struct {
	Header  Record
	Rows    []Record
	Records []Record
}

// Limits 是三类可配置上限；0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Builder 实现 lexer.Emitter，负责组装、列数一致性与上限判定。
// 同一 Builder 非并发安全；par 按段顺序重放事件到同一个 Builder。
type Builder struct {
	lim    Limits
	recs   []Record
	cur    Record
	nf     int
	err    *lexer.Error
	carry  *cell.Cell
	carryV []byte
}

// NewBuilder 创建组装器。
func NewBuilder(lim Limits) *Builder { return &Builder{lim: lim} }

// Table 返回已完成记录（错误发生时保留此前所有完整记录）。
func (b *Builder) Table() *Table {
	t := &Table{Records: b.recs}
	if len(b.recs) > 0 {
		t.Header, t.Rows = b.recs[0], b.recs[1:]
	}
	return t
}

// Err 返回终止错误。
func (b *Builder) Err() *lexer.Error { return b.err }

// Carry 返回段末开放字段。
func (b *Builder) Carry() (*cell.Cell, []byte) { return b.carry, b.carryV }

// SetCarry 在重放下一段前注入上段的开放字段。
func (b *Builder) SetCarry(c *cell.Cell, v []byte) {
	b.carry, b.carryV = c, append([]byte(nil), v...)
}

// CarryLen 返回当前 carry 的逻辑字节数（par 跨段字段计数用）。
func (b *Builder) CarryLen() int { return len(b.carryV) }

// MaterializeCarry 在段首行尾先于字段到达时，把 carry 落成一个字段。
func (b *Builder) MaterializeCarry() {
	if b.carry == nil {
		return
	}
	c := cell.Cell{Value: string(b.carryV), Quoted: b.carry.Quoted,
		Start: b.carry.Start, End: b.carry.Start + len(b.carryV)}
	b.carry, b.carryV = nil, nil
	_ = b.Field(c)
}

// Fail 以当前记录/字段号构造终止错误（par 边界特例用）。
func (b *Builder) Fail(k lexer.Kind, off int) *lexer.Error {
	return b.fail(k, off, b.nf+1)
}

// Bad 记录终止错误。
func (b *Builder) Bad(e *lexer.Error) bool {
	if b.err == nil {
		e.Record = len(b.recs) + 1
		if e.Field == 0 {
			e.Field = b.nf + 1
		}
		b.err = e
	}
	return true
}

// Field 接收一个完成字段。
func (b *Builder) Field(c cell.Cell) error {
	if b.err != nil {
		return b.err
	}
	if b.carry != nil {
		c.Start = b.carry.Start
		c.Quoted = b.carry.Quoted || c.Quoted
		c.Value = string(b.carryV) + c.Value
		b.carry, b.carryV = nil, nil
	}
	if b.lim.MaxFields > 0 && b.nf+1 > b.lim.MaxFields {
		return b.fail(lexer.TooManyFields, c.Start, b.nf+1)
	}
	b.nf++
	b.cur = append(b.cur, c)
	return nil
}

// RecordEnd 接收行尾；blank=true 的空行被跳过。
func (b *Builder) RecordEnd(blank bool) error {
	if b.err != nil {
		return b.err
	}
	b.carry, b.carryV = nil, nil
	if blank {
		b.cur, b.nf = nil, 0
		return nil
	}
	if b.lim.MaxRecords > 0 && len(b.recs)+1 > b.lim.MaxRecords {
		return b.fail(lexer.TooManyRecords, 0, b.nf+1)
	}
	if len(b.recs) > 0 && len(b.cur) != len(b.recs[0]) {
		return b.fail(lexer.ColumnCount, 0, len(b.cur)+1)
	}
	b.recs = append(b.recs, b.cur)
	b.cur, b.nf = nil, 0
	return nil
}

func (b *Builder) fail(k lexer.Kind, off, fld int) error {
	e := &lexer.Error{Kind: k, Offset: off, Record: len(b.recs) + 1, Field: fld}
	b.err = e
	return e
}

// sink 把 lexer 错误转交 Builder，保证错误带全局记录/字段号。
type sink struct{ b *Builder }

func (s sink) Field(c cell.Cell) error    { return s.b.Field(c) }
func (s sink) RecordEnd(blank bool) error { return s.b.RecordEnd(blank) }
func (s sink) Bad(e *lexer.Error) bool    { return s.b.Bad(e) }

// Parse 单线程流式解析整个输入。
func Parse(p []byte, lim Limits) (*Table, *lexer.Error) {
	b := NewBuilder(lim)
	l := lexer.New(sink{b}, lexer.Limits{MaxFieldBytes: lim.MaxFieldBytes, MaxFields: lim.MaxFields})
	if e := l.Feed(p); e != nil {
		return b.Table(), b.Err()
	}
	_ = l.Close()
	return b.Table(), b.Err()
}

// NewLexer 暴露给 par：创建共享组装器的状态机。
func NewLexer(b *Builder, lim Limits) *lexer.Lexer {
	return lexer.New(sink{b}, lexer.Limits{MaxFieldBytes: lim.MaxFieldBytes, MaxFields: lim.MaxFields})
}
