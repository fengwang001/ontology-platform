// Package table 把词法事件组装成表，负责列数一致性与行号定位。
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

// Table 是解析结果；Header 为第一条记录。
type Table struct {
	Header  []cell.Cell
	Records [][]cell.Cell
}

// Builder 是 lexer.Sink：收集事件并做列数检查，可用于流式或 par 重放。
type Builder struct {
	cur      []cell.Cell
	rows     [][]cell.Cell
	width    int
	recNo    int
	complete int // 已完整产出（闭合）的记录数
	fatal    error
	lim      cell.Limits
}

// NewBuilder 构造事件收集器。
func NewBuilder(lim cell.Limits) *Builder { return &Builder{lim: lim} }

// Field 实现 lexer.Sink。
func (b *Builder) Field(c cell.Cell) { b.cur = append(b.cur, c) }

// RecordEnd 实现 lexer.Sink；空行（零字段）在此被跳过。
func (b *Builder) RecordEnd(offset int) {
	row := b.cur
	b.cur = nil
	b.recNo++
	if len(row) == 0 {
		return // 空行：两 LF 相邻，跳过
	}
	if b.width == 0 {
		b.width = len(row)
	} else if len(row) != b.width && b.fatal == nil {
		b.fatal = &cell.PosError{Err: cell.ErrColumnCount, Offset: offset, Record: len(b.rows) + 1, Field: len(row)}
		return
	}
	if b.lim.MaxRecords > 0 && len(b.rows) >= b.lim.MaxRecords && b.fatal == nil {
		b.fatal = &cell.PosError{Err: cell.ErrTooManyRecords, Offset: offset, Record: len(b.rows) + 1, Field: 1}
		return
	}
	b.rows = append(b.rows, row)
	b.complete = len(b.rows)
}

// Complete 返回已完整产出的记录数（截断前缀性用）。
func (b *Builder) Complete() int { return b.complete }

// Table 取结果（终态错误下仍保留已产出记录）。
func (b *Builder) Table() *Table {
	if len(b.rows) == 0 {
		return &Table{}
	}
	return &Table{Header: b.rows[0], Records: b.rows[1:]}
}

// Err 返回列数等 table 层错误。
func (b *Builder) Err() error { return b.fatal }

// Parser 是单线程流式解析器（非并发安全）。
type Parser struct {
	mach *lexer.Machine
	bld  *Builder
}

// NewParser 构造流式解析器。
func NewParser(lim cell.Limits) *Parser {
	b := NewBuilder(lim)
	return &Parser{mach: lexer.New(b, lim, false, false, false), bld: b}
}

// Feed 送入一段字节。
func (p *Parser) Feed(d []byte) error {
	if err := p.mach.Feed(d); err != nil {
		return err
	}
	return p.bld.Err()
}

// Close 结束流。
func (p *Parser) Close() error {
	if err := p.mach.Close(); err != nil {
		return err
	}
	return p.bld.Err()
}

// Table 返回已解析的表。
func (p *Parser) Table() *Table { return p.bld.Table() }

// Parse 一次性解析。
func Parse(d []byte, lim cell.Limits) (*Table, error) {
	p := NewParser(lim)
	if err := p.Feed(d); err != nil {
		return p.Table(), err
	}
	if err := p.Close(); err != nil {
		return p.Table(), err
	}
	return p.Table(), nil
}

// IsColumnCount 判定是否列数不一致。
func IsColumnCount(err error) bool { return errors.Is(err, cell.ErrColumnCount) }
