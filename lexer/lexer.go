// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"
	"fmt"
	"strings"

	"ontology/cell"
)

// 四类语法错误 + 三类上限错误，彼此可判定。
var (
	ErrQuoteInUnquoted = errors.New("lexer: bare quote in unquoted field")
	ErrTextAfterQuote  = errors.New("lexer: data after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field")
	ErrLoneCR          = errors.New("lexer: bare carriage return")
	ErrFieldTooLong    = errors.New("lexer: field exceeds max bytes")
	ErrTooManyFields   = errors.New("lexer: record exceeds max fields")
	ErrTooManyRecords  = errors.New("lexer: too many records")
)

// Limits 为 0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Error 携带字节偏移、记录号、字段号（均从 1 起；字节偏移从 0 起）。
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

// Sink 接收词法事件；返回非 nil 即终结解析（上限错误从这里来）。
type Sink interface {
	Field(c cell.Cell) error
	EndRecord(offset int) error
	BlankLine() error
	Syntax(kind error, offset, record, field int) error
}

const (
	stFieldStart = iota
	stUnquoted
	stQuoted
	stQuoteSeen
	stCR
)

// Lexer 是流式状态机，单实例非并发安全。
type Lexer struct {
	sink   Sink
	lim    Limits
	bytes  int // 已处理字节总数（每个输入字节恰好一次）
	off    int // 全局字节位置
	state  int
	rec    int // 已结束记录数
	fld    int // 本记录已结束字段数
	b      strings.Builder
	start  int
	quoted bool
	has    bool // 当前字段是否已有任何字符/引号
	open   bool // 是否已开始一个字段（含空 "" 与未引号空串进行中）
	croff  int
	fatal  error
}

// New 构造解析器。
func New(sink Sink, lim Limits) *Lexer { return &Lexer{sink: sink, lim: lim} }

// 入口状态（供 par 段解析使用）。
const (
	EntryFresh     = stFieldStart
	EntryQuoted    = stQuoted
	EntryUnquoted  = stUnquoted
	EntryQuoteSeen = stQuoteSeen
	EntryCRPending = stCR
)

// Init 是一段解析的注入初始状态；Prefix 为跨段进行中字段的已解码值。
type Init struct {
	State  int
	Rec    int
	Fld    int
	Off    int
	Open   bool
	Quoted bool
	Start  int
	CROff  int
	Prefix string
}

// Carry 是段结尾仍跨段未闭合的字段/CR 状态。
type Carry struct {
	State  int
	Open   bool
	Quoted bool
	Start  int
	CROff  int
	Value  string
}

// Event 是段内一次词法动作；Offset 为全局字节偏移。
type Event struct {
	Kind   int // 1=Field 2=EndRecord 3=BlankLine 4=Syntax
	Cell   cell.Cell
	Syntax error
	Offset int
}

// SegmentResult 是一段在指定入口假设下跑完的结果。
type SegmentResult struct {
	Carry  Carry
	Events []Event
	Fatal  *Error
}

type recSink struct{ ev []Event }

func (r *recSink) Field(c cell.Cell) error { r.ev = append(r.ev, Event{Kind: 1, Cell: c}); return nil }
func (r *recSink) EndRecord(off int) error {
	r.ev = append(r.ev, Event{Kind: 2, Offset: off})
	return nil
}
func (r *recSink) BlankLine() error        { r.ev = append(r.ev, Event{Kind: 3}); return nil }
func (r *recSink) Syntax(k error, off, rec, fld int) error {
	r.ev = append(r.ev, Event{Kind: 4, Syntax: k, Offset: off})
	return &Error{Kind: k, Offset: off, Record: rec, Field: fld}
}

// RunWith 从指定初始状态解析一段，事件偏移均为全局坐标。F/Q 双假设各调一次。
func RunWith(seg []byte, lim Limits, in Init) SegmentResult {
	rs := &recSink{}
	l := &Lexer{sink: rs, lim: lim, off: in.Off, rec: in.Rec, fld: in.Fld,
		state: in.State, open: in.Open, quoted: in.Quoted, start: in.Start, croff: in.CROff}
	if in.Prefix != "" {
		l.b.WriteString(in.Prefix)
		l.has = true
	}
	l.Feed(seg)
	res := SegmentResult{Events: rs.ev}
	if e, ok := l.fatal.(*Error); ok {
		res.Fatal = e
	}
	res.Carry = Carry{State: l.state, Open: l.open, Quoted: l.quoted,
		Start: l.start, CROff: l.croff, Value: l.b.String()}
	return res
}

// BytesProcessed 返回状态机处理过的字节总数。
func (l *Lexer) BytesProcessed() int { return l.bytes }

// Fatal 返回终结错误（未终结为 nil）。
func (l *Lexer) Fatal() error { return l.fatal }

// Feed 送入任意长度的一段字节。
func (l *Lexer) Feed(p []byte) error {
	for _, ch := range p {
		l.bytes++
		l.step(ch)
		if l.fatal != nil {
			return l.fatal
		}
	}
	return nil
}

func (l *Lexer) fail(kind error, offset int) {
	if l.fatal == nil {
		l.fatal = l.sink.Syntax(kind, offset, l.rec+1, l.fld+1)
		if l.fatal == nil {
			l.fatal = &Error{Kind: kind, Offset: offset, Record: l.rec + 1, Field: l.fld + 1}
		}
	}
}

func (l *Lexer) addByte(ch byte, at int) bool {
	if l.lim.MaxFieldBytes > 0 && l.b.Len() >= l.lim.MaxFieldBytes {
		l.fail(ErrFieldTooLong, at)
		return false
	}
	l.b.WriteByte(ch)
	return true
}

func (l *Lexer) emitField(end int) bool {
	c := cell.Cell{Value: l.b.String(), Quoted: l.quoted, Start: l.start, End: end}
	l.b.Reset()
	l.state = stFieldStart
	l.has = false
	l.quoted = false
	l.open = false
	if l.lim.MaxFields > 0 && l.fld+1 > l.lim.MaxFields {
		l.fail(ErrTooManyFields, end)
		return false
	}
	if err := l.sink.Field(c); err != nil {
		l.fatal = err
		return false
	}
	l.fld++
	return true
}

func (l *Lexer) endRecord(at int) bool {
	l.state = stFieldStart
	if l.lim.MaxRecords > 0 && l.rec+1 > l.lim.MaxRecords {
		l.fail(ErrTooManyRecords, at)
		return false
	}
	if err := l.sink.EndRecord(at); err != nil {
		l.fatal = err
		return false
	}
	l.rec++
	l.fld = 0
	return true
}

func (l *Lexer) step(ch byte) {
	at := l.off
	l.off++
	switch l.state {
	case stFieldStart:
		l.start = at
		l.open = true
		switch ch {
		case ',':
			if !l.emitField(at + 1) {
				return
			}
		case '"':
			l.state, l.quoted, l.has = stQuoted, true, true
		case '\n':
			l.open = false
			l.sink.BlankLine()
		case '\r':
			l.open = false
			l.state, l.croff = stCR, at
		default:
			if !l.addByte(ch, at) {
				return
			}
			l.state, l.has = stUnquoted, true
		}
	case stUnquoted:
		switch ch {
		case ',':
			if !l.emitField(at + 1) {
				return
			}
		case '"':
			l.fail(ErrQuoteInUnquoted, at)
		case '\n':
			if !l.emitField(at) || !l.endRecord(at) {
				return
			}
		case '\r':
			l.state, l.croff = stCR, at
		default:
			if !l.addByte(ch, at) {
				return
			}
		}
	case stQuoted:
		switch ch {
		case '"':
			l.state = stQuoteSeen
		default:
			if !l.addByte(ch, at) {
				return
			}
		}
	case stQuoteSeen:
		switch ch {
		case '"':
			if !l.addByte('"', at) {
				return
			}
			l.state = stQuoted
		case ',':
			if !l.emitField(at + 1) {
				return
			}
		case '\n':
			if !l.emitField(at) || !l.endRecord(at) {
				return
			}
		case '\r':
			l.state, l.croff = stCR, at
		default:
			l.fail(ErrTextAfterQuote, at)
		}
	case stCR:
		if ch == '\n' {
			if l.has || l.quoted || l.open {
				if !l.emitField(at) || !l.endRecord(at) {
					return
				}
			} else {
				l.sink.BlankLine()
			}
		} else {
			l.fail(ErrLoneCR, l.croff)
		}
	}
}

// Close 结束流，刷出最后一条无换行记录或报告未闭合引号/孤立 CR。
func (l *Lexer) Close() error {
	if l.fatal != nil {
		return l.fatal
	}
	switch l.state {
	case stQuoted, stQuoteSeen:
		l.fail(ErrUnclosedQuote, l.off)
	case stCR:
		l.fail(ErrLoneCR, l.croff)
	default:
		if l.has || l.quoted || l.open || l.fld > 0 {
			l.emitField(l.off)
			if l.fatal == nil {
				l.endRecord(l.off)
			}
		}
	}
	return l.fatal
}
