package lexer

import (
	"errors"

	"ontology/cell"
)

// 语法与上限哨兵错误。
var (
	ErrQuote           = errors.New("unexpected quote in unquoted field")
	ErrCharsAfterQuote = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("unclosed quoted field")
	ErrBareCR          = errors.New("bare carriage return")
	ErrFieldTooLarge   = errors.New("field exceeds byte limit")
	ErrTooManyFields   = errors.New("record exceeds field count limit")
)

// ErrEvent 携带语法/上限错误及其全局字节偏移。
type ErrEvent struct {
	Err error
	Off int64
}

// Limits 是词法器可配置上限，0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
}

// Sink 接收词法事件。
type Sink interface {
	Cell(c cell.Cell)
	EndRecord()
	Error(e ErrEvent)
}

// Phase 是状态机出口状态。
type Phase int

const (
	PRecordStart Phase = iota // 记录边界（上一字节是换行或初始）
	PFieldStart               // 字段起始（逗号之后，记录内）
	PMidUnquoted
	PMidQuoted
	PQuoteSeen
	PCRPending
)

// Exit 描述切点处的出口，供 par 拼接。
type Exit struct {
	Phase Phase
	Tail  cell.Cell // PCRPending / PMidUnquoted / PMidQuoted / PQuoteSeen 时的待定字段
}

type Lexer struct {
	sink   Sink
	base   int64 // 本段第一个字节的全局偏移
	lim    Limits
	touch  int64
	fatal  *ErrEvent
	phase  Phase
	val    []byte
	start  int64
	last   int64 // 已处理的最后一个字节全局偏移；无输入时为 base-1
	quoted bool
	anchor bool // 本记录是否已见过任何字段锚点
	fields int  // 本记录已封口字段数
}

func New(sink Sink, base int64, lim Limits) *Lexer {
	return &Lexer{sink: sink, base: base, start: base, last: base - 1, phase: PRecordStart}
}

func (l *Lexer) fail(err error, off int64) error {
	if l.fatal == nil {
		l.fatal = &ErrEvent{Err: err, Off: off}
		l.sink.Error(*l.fatal)
	}
	return l.fatal.Err
}

func (l *Lexer) emitCell(off int64) {
	l.sink.Cell(cell.Cell{
		Value:  string(l.val),
		Quoted: l.quoted,
		Start:  l.start,
		End:    off,
	})
	l.fields++
	l.val = l.val[:0]
}

func (l *Lexer) addByte(b byte, off int64) error {
	if l.lim.MaxFieldBytes > 0 && len(l.val) >= l.lim.MaxFieldBytes {
		return l.fail(ErrFieldTooLarge, off)
	}
	l.val = append(l.val, b)
	return nil
}

func (l *Lexer) Feed(p []byte) error {
	if l.fatal != nil {
		return l.fatal.Err
	}
	for i := 0; i < len(p); i++ {
		l.touch++
		b := p[i]
		off := l.base + int64(i)
		l.last = off
		switch l.phase {
		case PRecordStart:
			l.resetRecord(off)
			switch b {
			case ',':
				if err := l.comma(off); err != nil {
					return err
				}
			case '"':
				l.quoted = true
				l.anchor = true
				l.phase = PMidQuoted
			case '\n':
			case '\r':
				l.phase = PCRPending
			default:
				if err := l.addByte(b, off); err != nil {
					return err
				}
				l.phase = PMidUnquoted
			}
		case PFieldStart:
			l.quoted = false
			l.start = off
			switch b {
			case ',':
				if err := l.comma(off); err != nil {
					return err
				}
			case '"':
				l.quoted = true
				l.phase = PMidQuoted
			case '\n':
				l.endRecord(off)
				l.phase = PRecordStart
			case '\r':
				l.phase = PCRPending
			default:
				if err := l.addByte(b, off); err != nil {
					return err
				}
				l.phase = PMidUnquoted
			}
		case PMidUnquoted:
			switch b {
			case ',':
				if err := l.comma(off); err != nil {
					return err
				}
			case '"':
				return l.fail(ErrQuote, off)
			case '\n':
				l.emitCell(off)
				l.endRecord()
				l.phase = PRecordStart
			case '\r':
				l.phase = PCRPending
			default:
				if err := l.addByte(b, off); err != nil {
					return err
				}
			}
		case PMidQuoted:
			switch b {
			case '"':
				l.phase = PQuoteSeen
			default:
				if err := l.addByte(b, off); err != nil {
					return err
				}
			}
		case PQuoteSeen:
			switch b {
			case ',':
				l.emitCell(off)
				if err := l.comma(off); err != nil {
					return err
				}
			case '"':
				if err := l.addByte('"', off); err != nil {
					return err
				}
				l.phase = PMidQuoted
			case '\n':
				l.emitCell(off - 1)
				l.endRecord()
				l.phase = PRecordStart
			case '\r':
				return l.fail(ErrBareCR, off)
			default:
				return l.fail(ErrCharsAfterQuote, off)
			}
		case PCRPending:
			switch b {
			case '\n':
				if l.anchor {
					l.emitCell(off - 1)
					l.endRecord()
				}
				l.phase = PRecordStart
			default:
				return l.fail(ErrBareCR, off-1)
			}
		}
		if l.fatal != nil {
			return l.fatal.Err
		}
	}
	return nil
}

func (l *Lexer) resetRecord(off int64) {
	l.anchor = false
	l.quoted = false
	l.start = off
	l.fields = 0
	l.val = l.val[:0]
}

func (l *Lexer) comma(off int64) error {
	if l.phase != PQuoteSeen {
		l.emitCell(off)
	}
	l.anchor = true
	if l.lim.MaxFields > 0 && l.fields+1 > l.lim.MaxFields {
		return l.fail(ErrTooManyFields, off)
	}
	l.phase = PFieldStart
	return nil
}

func (l *Lexer) endRecord() {
	l.anchor = true
	l.sink.EndRecord()
}

func (l *Lexer) Close() error {
	if l.fatal != nil {
		return l.fatal.Err
	}
	switch l.phase {
	case PMidQuoted:
		return l.fail(ErrUnclosedQuote, l.last)
	case PCRPending:
		return l.fail(ErrBareCR, l.last)
	case PMidUnquoted:
		l.emitCell(l.last + 1)
		l.sink.EndRecord()
	case PFieldStart:
		l.emitCell(l.last + 1)
		l.sink.EndRecord()
	case PQuoteSeen:
		l.emitCell(l.last)
		l.sink.EndRecord()
	case PRecordStart:
	}
	return nil
}

func (l *Lexer) Touches() int64 { return l.touch }
