// Package table 把 lexer 事件组装成表，负责表头、列数一致性与记录上限。
package table

import (
	"ontology/cell"
	"ontology/lexer"
)

// Table 是成功解析出的表。Header 为第一条保留记录。
type Table struct {
	Header  []cell.Cell
	Records [][]cell.Cell
}

// Width 返回列数。
func (t *Table) Width() int { return len(t.Header) }

// Builder 实现 lexer.Sink，按全局顺序赋予记录号/字段号。
type Builder struct {
	tab      Table
	cur      []cell.Cell
	err      *cell.PosError
	finished bool
	lim      cell.Limits
}

// NewBuilder 创建组装器。
func NewBuilder(lim cell.Limits) *Builder {
	return &Builder{lim: lim}
}

// Cell 实现 lexer.Sink。
func (b *Builder) Cell(c cell.Cell) { b.cur = append(b.cur, c) }

// EndRec 实现 lexer.Sink：空行（0 字段）跳过；首条记录定义列数。
func (b *Builder) EndRec() {
	row := b.cur
	b.cur = nil
	if len(row) == 0 {
		return
	}
	if b.tab.Header == nil {
		b.tab.Header = row
		return
	}
	if len(row) != len(b.tab.Header) {
		b.fail(cell.ErrColumnCount, row[len(row)-1].End)
		return
	}
	if b.lim.MaxRecords > 0 && len(b.tab.Records)+1 > b.lim.MaxRecords {
		b.fail(cell.ErrTooManyRecords, row[0].Start)
		return
	}
	b.tab.Records = append(b.tab.Records, row)
}

// Error 实现 lexer.Sink，为词法错误补上记录号、字段号。
func (b *Builder) Error(e *cell.PosError) {
	if b.err == nil {
		e.Record = len(b.tab.Records) + 1
		if b.tab.Header == nil {
			e.Record = 1
		}
		e.Field = len(b.cur) + 1
		b.err = e
	}
}

func (b *Builder) fail(err error, off int) {
	pe := &cell.PosError{
		Err: err, Offset: off,
		Record: len(b.tab.Records) + 1,
		Field:  len(b.cur) + 1,
	}
	if b.tab.Header == nil {
		pe.Record, pe.Field = 1, len(b.cur)+1
	}
	b.Error(pe)
}

// Result 返回表与首个错误（无错误返回 nil）。
func (b *Builder) Result() (*Table, *cell.PosError) {
	return &b.tab, b.err
}

// Error 返回首个错误。
func (b *Builder) Err() *cell.PosError { return b.err }

// Table 别名导出便于读取。

// Parse 一次性解析整个缓冲区。
func Parse(buf []byte, lim cell.Limits) (*Table, *cell.PosError) {
	b := NewBuilder(lim)
	lx := lexer.New(b, lim)
	if err := lx.Feed(buf); err != nil {
		return b.Result()
	}
	if err := lx.Close(); err != nil {
		return b.Result()
	}
	return b.Result()
}

// FeedParser 是支持半包续传的流式解析器。单实例非并发安全。
type FeedParser struct {
	b  *Builder
	lx *lexer.Lexer
}

// NewFeedParser 创建流式解析器。
func NewFeedParser(lim cell.Limits) *FeedParser {
	b := NewBuilder(lim)
	return &FeedParser{b: b, lx: lexer.New(b, lim)}
}

// Feed 追加一段输入。
func (p *FeedParser) Feed(data []byte) error { return p.lx.Feed(data) }

// Close 结束输入。
func (p *FeedParser) Close() error { return p.lx.Close() }

// Result 取当前结果。
func (p *FeedParser) Result() (*Table, *cell.PosError) { return p.b.Result() }

// Steps 返回字节处理计数。
func (p *FeedParser) Steps() int64 { return p.lx.Steps() }
