// Package lexer 是可暂停续传的逐字节 CSV 状态机，每字节恰好处理一次。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 四类语法错误与三类上限错误，彼此可用 errors.Is 区分。
var (
	ErrBareQuote      = errors.New("bare quote in unquoted field")
	ErrQuoteJunk      = errors.New("junk after closed quote")
	ErrUnterminated   = errors.New("unterminated quoted field")
	ErrBareCR         = errors.New("bare carriage return")
	ErrFieldTooLarge  = errors.New("field exceeds byte limit")
	ErrTooManyFields  = errors.New("record exceeds field limit")
	ErrTooManyRecords = errors.New("record count exceeds limit")
)

// Error 携带字节偏移、记录号、字段号（均从 1 起，偏移从 0 起）。
type Error struct {
	Kind   error
	Offset int64
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

// State 是状态机状态。
type State int

const (
	FState State = iota // 字段开始
	UState              // 未引号字段中
	QState              // 引号字段中
	AState              // 引号字段中刚见到一个引号
	CState              // 行尾 CR 待定
)

// Limits 为 0 的项表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Sink 接收字段与行结束事件。
type Sink interface {
	Cell(c cell.Cell) error
	EndRecord(termOff int64) error
}

// Event 是供 par 拼接的事件：字段或行结束。
type Event struct {
	C         cell.Cell
	FieldNo   int
	RecNo     int
	RecordEnd bool
	TermOff   int64
}

// Snap 是段边界快照（坐标为段内局部坐标）。
type Snap struct {
	State    State
	Buf      []byte
	FieldOff int64
	Quoted   bool
	Open     bool
	Fields   int
	RecNo    int
	Recs     int
}

// Lexer 是流式状态机；单实例非并发安全。
type Lexer struct {
	sink   Sink
	lim    Limits
	events []Event
	err    error
	closed bool
	state  State
	buf    []byte
	off    int64
	fieldO int64
	quoted bool
	open   bool
	fields int
	recNo  int
	recs   int
	crOff  int64

	// Count 记录字节被处理的总次数。
	Count int64
}

// New 创建流式词法器。
func New(sink Sink, lim Limits) *Lexer {
	return &Lexer{sink: sink, lim: lim, recNo: 1}
}

// BytesProcessed 返回字节被状态机处理的总次数。
func (l *Lexer) BytesProcessed() int64 { return l.Count }

// Snapshot 返回当前状态快照（坐标为当前流的局部坐标）。
func (l *Lexer) Snapshot() Snap {
	return Snap{State: l.state, Buf: append([]byte(nil), l.buf...), FieldOff: l.fieldO,
		Quoted: l.quoted, Open: l.open, Fields: l.fields, RecNo: l.recNo, Recs: l.recs}
}

// ParseFragment 供 par 使用：从 start 快照起解析 p。final 为最后一段时套用 EOF 规则。
// 只强制字段字节上限（靠 start.Buf 携带跨界长度）；字段数/记录数上限由拼接方统一判定。
func ParseFragment(p []byte, start Snap, final bool, lim Limits) ([]Event, Snap, error) {
	l := &Lexer{state: start.State, buf: append([]byte(nil), start.Buf...),
		fieldO: start.FieldOff, quoted: start.Quoted, open: start.Open,
		fields: start.Fields, recNo: start.RecNo, recs: start.Recs,
		lim: Limits{MaxFieldBytes: lim.MaxFieldBytes}, events: []Event{}}
	err := l.Feed(p)
	if err == nil && final {
		err = l.Close()
	}
	return l.events, l.Snapshot(), err
}

func (l *Lexer) fail(kind error, off int64) error {
	if l.err == nil {
		l.err = &Error{Kind: kind, Offset: off, Record: l.recNo, Field: l.fields + 1}
}
	return l.err
}

func (l *Lexer) emit(off int64) error {
	c := cell.Cell{Value: string(l.buf), Quoted: l.quoted, Off: l.fieldO, End: off}
	l.fields++
	if l.events != nil {
		l.events = append(l.events, Event{C: c, FieldNo: l.fields, RecNo: l.recNo})
	} else if err := l.sink.Cell(c); err != nil {
		return err
	}
	if l.lim.MaxFields > 0 && l.fields > l.lim.MaxFields {
		return l.fail(ErrTooManyFields, off)
	}
	l.buf, l.open, l.quoted = l.buf[:0], false, false
	return nil
}

func (l *Lexer) endRec(termOff int64) error {
	if l.fields == 0 && !l.open { // 空行：跳过
		l.recNo++
		return nil
	}
	if l.open {
		if err := l.emit(termOff); err != nil {
			return err
		}
	}
	l.recs++
	if l.lim.MaxRecords > 0 && l.recs > l.lim.MaxRecords {
		return l.fail(ErrTooManyRecords, termOff)
	}
	if l.events != nil {
		l.events = append(l.events, Event{RecordEnd: true, TermOff: termOff, RecNo: l.recNo})
	} else if err := l.sink.EndRecord(termOff); err != nil {
		return err
	}
	l.fields, l.recNo = 0, l.recNo+1
	return nil
}

func (l *Lexer) add(b byte, at int64) error {
	l.buf = append(l.buf, b)
	if l.lim.MaxFieldBytes > 0 && len(l.buf) > l.lim.MaxFieldBytes {
		return l.fail(ErrFieldTooLarge, at)
	}
	return nil
}

// Feed 喂入一段字节，可调用任意次。
func (l *Lexer) Feed(p []byte) error {
	if l.err != nil {
		return l.err
	}
	for i := 0; i < len(p); i++ {
		l.Count++
		at := l.off
		l.off++
		b := p[i]
		switch l.state {
		case FState:
			switch b {
			case ',':
				if err := l.emit(at); err != nil {
					return err
				}
			case '"':
				l.state, l.open, l.quoted, l.fieldO = QState, true, true, at
			case '\r':
				l.state, l.crOff = CState, at
			case '\n':
				l.recNo++ // 空行跳过
			default:
				l.state, l.open, l.fieldO = UState, true, at
				if err := l.add(b, at); err != nil {
					return err
				}
			}
		case UState:
			switch b {
			case ',':
				if err := l.emit(at); err != nil {
					return err
				}
				l.state = FState
			case '"':
				return l.fail(ErrBareQuote, at)
			case '\r':
				l.state, l.crOff = CState, at
			case '\n':
				if err := l.endRec(at); err != nil {
					return err
				}
				l.state = FState
			default:
				if err := l.add(b, at); err != nil {
					return err
				}
			}
		case QState:
			switch b {
			case '"':
				l.state = AState
			default: // 逗号、\r、\n、普通字节均原样保留
				if err := l.add(b, at); err != nil {
					return err
				}
			}
		case AState:
			switch b {
			case '"':
				l.state = QState
				if err := l.add(b, at); err != nil {
					return err
				}
			case ',':
				if err := l.emit(at); err != nil {
					return err
				}
				l.state = FState
			case '\r':
				l.state, l.crOff = CState, at
			case '\n':
				if err := l.endRec(at); err != nil {
					return err
				}
				l.state = FState
			default:
				return l.fail(ErrQuoteJunk, at)
			}
		case CState:
			if b == '\n' {
				if err := l.endRec(l.crOff); err != nil {
					return err
				}
				l.state = FState
			} else {
				return l.fail(ErrBareCR, l.crOff)
			}
		}
	}
	return nil
}

// Close 结束流并按 EOF 规则定性。
func (l *Lexer) Close() error {
	if l.closed {
		return l.err
	}
	l.closed = true
	if l.err != nil {
		return l.err
	}
	switch l.state {
	case QState, AState:
		return l.fail(ErrUnterminated, l.off)
	case CState:
		return l.fail(ErrBareCR, l.crOff)
	case UState:
		return l.endRec(l.off)
	case FState:
		if l.fields > 0 {
			return l.endRec(l.off)
		}
	}
	return nil
}
