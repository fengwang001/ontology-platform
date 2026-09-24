// Package lexer 是可暂停、可续传的 CSV 逐字节状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

// State 是词法状态机状态。
type State int

const (
	StFieldStart State = iota // S0
	StBare                    // S1
	StQuoted                  // S2
	StQuoteSeen               // S3
	StCR                      // S4
)

// Config 配置词法层上限；0 表示不限制。
type Config struct {
	MaxFieldBytes int
	MaxFields     int
}

// Kind 标识四类语法错误与两类字段级上限错误。
type Kind int

const (
	BareQuote Kind = iota
	JunkAfterQuote
	UnclosedQuote
	DanglingCR
	FieldTooLarge
	TooManyFields
)

var (
	ErrBareQuote      = errors.New("bare quote in unquoted field")
	ErrJunkAfterQuote = errors.New("characters after closing quote")
	ErrUnclosedQuote  = errors.New("unclosed quoted field at EOF")
	ErrDanglingCR     = errors.New("bare carriage return")
	ErrFieldTooLarge  = errors.New("field exceeds max bytes")
	ErrTooManyFields  = errors.New("record exceeds max fields")
)

// Error 携带可判定种类与出错位置（偏移从 0 起，记录/字段从 1 起）。
type Error struct {
	Kind   Kind
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	names := [...]string{ErrBareQuote.Error(), ErrJunkAfterQuote.Error(),
		ErrUnclosedQuote.Error(), ErrDanglingCR.Error(),
		ErrFieldTooLarge.Error(), ErrTooManyFields.Error()}
	return names[e.Kind]
}

func (e *Error) Is(target error) bool {
	switch target {
	case ErrBareQuote:
		return e.Kind == BareQuote
	case ErrJunkAfterQuote:
		return e.Kind == JunkAfterQuote
	case ErrUnclosedQuote:
		return e.Kind == UnclosedQuote
	case ErrDanglingCR:
		return e.Kind == DanglingCR
	case ErrFieldTooLarge:
		return e.Kind == FieldTooLarge
	case ErrTooManyFields:
		return e.Kind == TooManyFields
	}
	return false
}

// Handler 接收词法事件。
type Handler interface {
	Field(c cell.Cell)
	Record()
	Error(e *Error)
}

// EventKind 区分日志事件。
type EventKind uint8

const (
	EvField EventKind = iota + 1
	EvRecord
	EvError
)

// Event 是一条词法事件（偏移为段内/全局相对坐标，视构造方式而定）。
type Event struct {
	Kind EventKind
	Cell cell.Cell
}

// Log 是收集事件的 Handler，供 table 与 par 使用。
type Log struct {
	Events []Event
	Err    *Error
}

func (l *Log) Field(c cell.Cell) { l.Events = append(l.Events, Event{Kind: EvField, Cell: c}) }
func (l *Log) Record()           { l.Events = append(l.Events, Event{Kind: EvRecord}) }
func (l *Log) Error(e *Error)    { l.Events = append(l.Events, Event{Kind: EvError}); l.Err = e }

// Snapshot 是状态机的完整可恢复快照。
type Snapshot struct {
	State    State
	Val      []byte
	Quoted   bool
	CurStart int
	Pos      int
	RecNo    int
	FldNo    int
	Err      *Error
}

// Lexer 是单次流式解析器；非并发安全。
type Lexer struct {
	cfg                         Config
	h                           Handler
	st                          State
	val                         []byte
	quoted                      bool
	curStart, pos, recNo, fldNo int
	err                         *Error
	processed                   int
}

// New 创建解析器。
func New(cfg Config, h Handler) *Lexer {
	return &Lexer{cfg: cfg, h: h}
}

// Resume 从快照恢复（Val 被复制）。
func Resume(s Snapshot, cfg Config, h Handler) *Lexer {
	v := make([]byte, len(s.Val))
	copy(v, s.Val)
	return &Lexer{cfg: cfg, h: h, st: s.State, val: v, quoted: s.Quoted,
		curStart: s.CurStart, pos: s.Pos, recNo: s.RecNo, fldNo: s.FldNo, err: s.Err}
}

// Snapshot 返回当前状态的深拷贝。
func (x *Lexer) Snapshot() Snapshot {
	v := make([]byte, len(x.val))
	copy(v, x.val)
	return Snapshot{x.st, v, x.quoted, x.curStart, x.pos, x.recNo, x.fldNo, x.err}
}

// Processed 返回状态机处理过的字节总数。
func (x *Lexer) Processed() int { return x.processed }

func (x *Lexer) fail(k Kind, off int) *Error {
	if x.err == nil {
		e := &Error{Kind: k, Offset: off, Record: x.recNo + 1, Field: x.fldNo}
		x.err = e
		x.h.Error(e)
	}
	return x.err
}

func (x *Lexer) emitField(end int) {
	x.h.Field(cell.Cell{Value: string(x.val), Quoted: x.quoted,
		Start: x.curStart, End: end})
	x.val = x.val[:0]
}

func (x *Lexer) endRecord() {
	x.recNo++
	x.h.Record()
	x.fldNo = 0
}

// Feed 送入任意长度的字节块。
func (x *Lexer) Feed(p []byte) error {
	if x.err != nil {
		return x.err
	}
	for _, b := range p {
		x.processed++
		at := x.pos
		x.pos++
		switch x.st {
		case StFieldStart:
			if x.fldNo == 0 {
				x.fldNo = 1
				x.curStart = at
			}
			switch b {
			case ',':
				x.emitField(at)
				x.afterComma(at)
			case '"':
				x.st, x.quoted = StQuoted, true
			case '\n':
				x.emitField(at)
				x.endRecord()
			case '\r':
				x.emitField(at)
				x.st = StCR
			default:
				x.st = StBare
				x.appendByte(b, at)
			}
		case StBare:
			switch b {
			case ',':
				x.emitField(at)
				x.afterComma(at)
			case '"':
				x.fail(BareQuote, at)
			case '\n':
				x.emitField(at)
				x.endRecord()
			case '\r':
				x.emitField(at)
				x.st = StCR
			default:
				x.appendByte(b, at)
			}
		case StQuoted:
			if b == '"' {
				x.st = StQuoteSeen
			} else {
				x.appendByte(b, at)
			}
		case StQuoteSeen:
			switch b {
			case '"':
				x.st = StQuoted
				x.appendByte('"', at)
			case ',':
				x.emitField(at)
				x.afterComma(at)
			case '\n':
				x.emitField(at)
				x.endRecord()
			case '\r':
				x.emitField(at)
				x.st = StCR
			default:
				x.fail(JunkAfterQuote, at)
			}
		case StCR:
			if b == '\n' {
				x.endRecord()
				x.st = StFieldStart
			} else {
				x.fail(DanglingCR, at)
			}
		}
		if x.err != nil {
			return x.err
		}
	}
	return nil
}

func (x *Lexer) afterComma(at int) {
	x.fldNo++
	if x.cfg.MaxFields > 0 && x.fldNo > x.cfg.MaxFields {
		x.fail(TooManyFields, at)
		return
	}
	x.quoted = false
	x.st = StFieldStart
	x.curStart = at + 1
}

func (x *Lexer) appendByte(b byte, at int) {
	if x.cfg.MaxFieldBytes > 0 && len(x.val)+1 > x.cfg.MaxFieldBytes {
		x.fail(FieldTooLarge, at)
		return
	}
	x.val = append(x.val, b)
}

// Close 宣告流结束。
func (x *Lexer) Close() error {
	if x.err != nil {
		return x.err
	}
	switch x.st {
	case StQuoted:
		return x.fail(UnclosedQuote, x.pos)
	case StCR:
		return x.fail(DanglingCR, x.pos)
	case StFieldStart:
		if x.fldNo > 0 {
			x.emitField(x.pos)
			x.endRecord()
		}
	default:
		x.emitField(x.pos)
		x.endRecord()
	}
	return nil
}
