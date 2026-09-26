// Package lexer 是一个可暂停、可续传、可从任意状态播种的 CSV 字节状态机。
package lexer

import (
	"fmt"

"ontology/cell"
)

// State 是状态机的持久状态，可跨 Feed 与 par 段边界保存恢复。
type State uint8

const (
	S0 State = iota // 字段开始
	S1              // 未引号字段中
	S2              // 引号字段中
	S3              // 引号内刚见引号
	S4              // 行尾 CR 待定
)

// Kind 标识可判定的错误类别。
type Kind int

const (
	KindBareQuote Kind = iota + 1
	KindTrailingQuote
	KindUnclosedQuote
	KindLoneCR
	KindFieldTooLarge
	KindTooManyFields
)

// Error 带出错字节偏移（从 0）、记录号、字段号（从 1）。
type Error struct {
	Kind   Kind
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("csv lexer error %d at byte %d record %d field %d", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// 哨兵错误，供 errors.Is 判定。
var (
	ErrBareQuote     = &Error{Kind: KindBareQuote}
	ErrTrailingQuote = &Error{Kind: KindTrailingQuote}
	ErrUnclosedQuote = &Error{Kind: KindUnclosedQuote}
	ErrLoneCR        = &Error{Kind: KindLoneCR}
	ErrFieldTooLarge = &Error{Kind: KindFieldTooLarge}
	ErrTooManyFields = &Error{Kind: KindTooManyFields}
)

// Event 是状态机产出的事件：一个完整字段或一条记录边界。
type Event struct {
	Field bool
	C     cell.Cell
}

// Seed 是某字节切点处的完整状态快照。
type Seed struct {
	State         State
	Offset        int // 已消费字节数（切点全局偏移）
	Records       int // 已完成记录数
	Fields        int // 当前记录已发出的字段数
	Started       bool
	Quoted        bool
	Start         int // 悬挂字段起点
	Len           int // 悬挂字段已解码长度
	CloseEnd      int // S3 中闭合引号之后的偏移
	CROffset      int // S4 中 CR 的偏移
	Prefix        string
	MaxFieldBytes int
	MaxFields     int
}

// Lexer 逐字节驱动；单实例非并发安全。
type Lexer struct {
	st     State
	off    int
	recs   int
	fields int
	started bool
	quoted bool
	start  int
	closeEnd int
	crOff  int
	buf    []byte
	maxFb  int
	maxF   int
	emit   func(Event)
	err    *Error
	n      int // 字节被状态机处理的总次数
}

// New 创建状态机；max* 为 0 表示不限。
func New(emit func(Event), maxFieldBytes, maxFields int) *Lexer {
	return &Lexer{emit: emit, maxFb: maxFieldBytes, maxF: maxFields, start: -1}
}

// NewSeeded 从快照恢复，用于 par 的分段 worker。
func NewSeeded(s Seed, emit func(Event)) *Lexer {
	l := &Lexer{st: s.State, off: s.Offset, recs: s.Records, fields: s.Fields,
		started: s.Started, quoted: s.Quoted, start: s.Start, closeEnd: s.CloseEnd,
		crOff: s.CROffset, maxFb: s.MaxFieldBytes, maxF: s.MaxFields, emit: emit}
	if s.Prefix != "" {
		l.buf = []byte(s.Prefix)
	}
	return l
}

// Seed 导出处在当前偏移的快照。
func (l *Lexer) Seed() Seed {
	return Seed{State: l.st, Offset: l.off, Records: l.recs, Fields: l.fields,
		Started: l.started, Quoted: l.quoted, Start: l.start, Len: len(l.buf),
		CloseEnd: l.closeEnd, CROffset: l.crOff, Prefix: string(l.buf),
		MaxFieldBytes: l.maxFb, MaxFields: l.maxF}
}

// Err 返回终态错误（无则 nil）。
func (l *Lexer) Err() *Error { return l.err }

// BytesProcessed 返回状态机处理过的字节总数。
func (l *Lexer) BytesProcessed() int { return l.n }

func (l *Lexer) fail(k Kind, off, rec, field int) *Error {
	if l.err == nil {
		l.err = &Error{Kind: k, Offset: off, Record: rec, Field: field}
	}
	return l.err
}

func (l *Lexer) add(b byte, pos int) bool {
	if l.maxFb > 0 && len(l.buf) >= l.maxFb {
		l.fail(KindFieldTooLarge, pos, l.recs+1, l.fields+1)
		return false
	}
	l.buf = append(l.buf, b)
	return true
}

// emitField 在当前偏移发出悬挂字段；emptyPos 用于未开始的空字段。
func (l *Lexer) emitField(off int) bool {
	if l.maxF > 0 && l.fields >= l.maxF {
		l.fail(KindTooManyFields, off, l.recs+1, l.fields+1)
		return false
	}
	st, q := l.start, l.quoted
	end := off
	if l.started && q {
		end = l.closeEnd
	}
	if !l.started {
		st, end = off, off
	}
	c := cell.Cell{Value: string(l.buf), Quoted: q, Start: st, End: end}
	l.fields++
	l.buf, l.started, l.quoted, l.start = l.buf[:0], false, false, -1
	l.emit(Event{Field: true, C: c})
	return true
}

func (l *Lexer) closeRecord() {
	l.recs++
	f := l.fields
	l.fields = 0
	l.emit(Event{C: cell.Cell{Start: f}})
}

// Feed 喂入一段字节；返回首个错误后进入终态。
func (l *Lexer) Feed(p []byte) error {
	for _, b := range p {
		if l.err != nil {
			return l.err
		}
		pos := l.off
		l.n++
		l.off++
		switch l.st {
		case S0:
			switch b {
			case ',':
				if !l.emitField(pos) {
					return l.err
				}
			case '\n':
			case '\r':
				l.st, l.crOff = S4, pos
			case '"':
				l.st, l.started, l.quoted, l.start = S2, true, true, pos
			default:
				if !l.add(b, pos) {
					return l.err
				}
				l.st, l.started, l.start = S1, true, pos
			}
		case S1:
			switch b {
			case ',':
				if !l.emitField(pos) {
					return l.err
				}
				l.st = S0
			case '\n':
				if !l.emitField(pos) || !l.okRec() {
					return l.err
				}
				l.st = S0
			case '\r':
				l.st, l.crOff = S4, pos
			case '"':
				l.fail(KindBareQuote, pos, l.recs+1, l.fields+1)
			default:
				if !l.add(b, pos) {
					return l.err
				}
			}
		case S2:
			switch b {
			case ',':
				if !l.add(b, pos) {
					return l.err
				}
			case '\n', '\r':
				if !l.add(b, pos) {
					return l.err
				}
			case '"':
				l.st, l.closeEnd = S3, pos+1
			default:
				if !l.add(b, pos) {
					return l.err
				}
			}
		case S3:
			switch b {
			case ',':
				if !l.emitField(pos) {
					return l.err
				}
				l.st = S0
			case '\n':
				if !l.emitField(pos) || !l.okRec() {
					return l.err
				}
				l.st = S0
			case '\r':
				l.st, l.crOff = S4, pos
			case '"':
				if !l.add('"', pos) {
					return l.err
				}
				l.st = S2
			default:
				l.fail(KindTrailingQuote, pos, l.recs+1, l.fields+1)
			}
		case S4:
			if b != '\n' {
				l.fail(KindLoneCR, l.crOff, l.recs+1, l.fields+1)
				break
			}
			if l.started {
				if !l.emitField(pos) || !l.okRec() {
					return l.err
				}
			}
			l.st = S0
		}
	}
	return l.err
}

func (l *Lexer) okRec() bool {
	if l.emit == nil {
		l.recs++
		l.fields = 0
		return true
	}
	l.closeRecord()
	return true
}

// End 宣告流结束，处理 EOF 语义。
func (l *Lexer) End() error {
	if l.err != nil {
		return l.err
	}
	switch l.st {
	case S2:
		l.fail(KindUnclosedQuote, l.off, l.recs+1, l.fields+1)
	case S4:
		l.fail(KindLoneCR, l.crOff, l.recs+1, l.fields+1)
	case S1, S3:
		if !l.emitField(l.off) {
			return l.err
		}
		l.okRec()
	}
	l.st = S0
	return l.err
}
