// Package lexer 是可暂停、可续传的 CSV 逐字节状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 四类可判定词法错误，加上字段超限；外层组装器另列结构上限。
var (
	ErrBareQuote    = errors.New("bare quote in unquoted field")
	ErrAfterQuote   = errors.New("unexpected char after closing quote")
	ErrUnclosed     = errors.New("unterminated quoted field")
	ErrLoneCR       = errors.New("lone carriage return")
	ErrFieldTooLong = errors.New("field exceeds max bytes")
	ErrTerminal     = errors.New("lexer already in terminal state")
)

// State 是状态机当前状态，也用于 par 段扫描的初始假设。
type State int

const (
	Start   State = iota // 字段开始
	Bare                 // 未引号字段中
	Quoted               // 引号字段中
	QEnd                 // 引号字段中刚见闭合引号
	CRPending            // 行尾 CR 待定
)

// Pos 是从 1 起的记录号/字段号与从 0 起的字节偏移。
type Pos struct {
	Offset int
	Record int
	Field  int
}

// Error 携带错误类别与位置。用 errors.As 判定 Kind。
type Error struct {
	Kind error
	Pos
}

func (e *Error) Error() string { return e.Kind.Error() + " at " + itoa(e.Offset) }
func (e *Error) Unwrap() error { return e.Kind }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Event 是段扫描产出的一个事件：要么一个字段，要么一条记录结束于 At。
type Event struct {
	Cell  cell.Cell
	IsRec bool
	At    int
}

// SegInit 初始化一次段扫描：起始状态与当前字段已累计的值字节数。
type SegInit struct {
	State    State
	FieldLen int
}

// Lexer 是增量状态机。单个实例不是并发安全的。
type Lexer struct {
	MaxFieldBytes int // 0 表示不限

	onField func(cell.Cell) error
	onRec   func(int) error

	state  State
	val    []byte
	quoted bool
	fstart int
	qstart int
	crpos  int
	qepos  int
	flen   int
	abs    int
	recNo  int
	fldNo  int

	BytesRead int64 // 非导出语义的计数器：被处理的字节总数

	started bool
	seen    bool
	dead    bool
	final   error

	// 段扫描结果收集
	Events []Event
}

// NewStream 创建增量解析用状态机，字段/记录事件回调到组装器。
func NewStream(maxField int, onField func(cell.Cell) error, onRec func(int) error) *Lexer {
	return &Lexer{MaxFieldBytes: maxField, onField: onField, onRec: onRec}
}

// NewSeg 创建一次性段扫描：从 base 绝对偏移、init 初始态开始，只收事件不做 EOF 收尾。
func NewSeg(base int, init SegInit, maxField int) *Lexer {
	l := &Lexer{MaxFieldBytes: maxField, state: init.State, flen: init.FieldLen, abs: base, fstart: base, recNo: 1, fldNo: 1}
	l.onField = func(c cell.Cell) error { l.Events = append(l.Events, Event{Cell: c}); return nil }
	l.onRec = func(at int) error { l.Events = append(l.Events, Event{IsRec: true, At: at}); return nil }
	return l
}

func (l *Lexer) fail(kind error, off int) error {
	e := &Error{Kind: kind, Pos: Pos{Offset: off, Record: l.recNo, Field: l.fldNo}}
	l.dead, l.final = true, e
	return e
}

// add 把一个值字节计入字段并执行「立刻」上限检查。
func (l *Lexer) add(b byte, at int) error {
	l.val = append(l.val, b)
	l.flen++
	if l.MaxFieldBytes > 0 && l.flen > l.MaxFieldBytes {
		return l.fail(ErrFieldTooLong, at)
	}
	return nil
}

func (l *Lexer) emit(end int) error {
	c := cell.New(string(l.val), l.quoted, l.fstart, end)
	l.val, l.quoted = nil, false
	l.flen = 0
	if l.onField != nil {
		if err := l.onField(c); err != nil {
			l.dead, l.final = true, err
			return err
		}
	}
	return nil
}

func (l *Lexer) endRec(at int) error {
	l.recNo++
	l.fldNo = 1
	l.started = false
	if l.onRec != nil {
		if err := l.onRec(at); err != nil {
			l.dead, l.final = true, err
			return err
		}
	}
	return nil
}

// Feed 送入任意长度的一段字节，可反复调用。
func (l *Lexer) Feed(p []byte) error {
	if l.dead {
		if l.final != nil {
			return l.final
		}
		return ErrTerminal
	}
	for _, b := range p {
		at := l.abs
		l.abs++
		l.BytesRead++
		l.seen = true
		switch l.state {
		case Start, Bare:
			switch {
			case b == ',':
				if err := l.emit(at); err != nil {
					return err
				}
				l.fldNo++
				l.fstart = at + 1
				l.started = true
			case b == '"":
				if l.state == Bare {
					return l.fail(ErrBareQuote, at)
				}
				l.state, l.quoted, l.qstart = Quoted, true, at
				l.started = true
			case b == '\n':
				if err := l.emit(at); err != nil {
					return err
				}
				if err := l.endRec(at); err != nil {
					return err
				}
				l.fstart = at + 1
			case b == '\r':
				if l.state == Start || l.state == Bare {
					if err := l.emit(at); err != nil {
						return err
					}
				}
				l.state, l.crpos = CRPending, at
			default:
				l.state = Bare
				l.started = true
				if err := l.add(b, at); err != nil {
					return err
				}
			}
		case Quoted:
			if b == '"' {
				l.state, l.qepos = QEnd, at
			} else {
				if err := l.add(b, at); err != nil {
					return err
				}
			}
		case QEnd:
			switch {
			case b == '"':
				l.state = Quoted
				if err := l.add('"', at); err != nil {
					return err
				}
			case b == ',':
				if err := l.emit(at); err != nil {
					return err
				}
				l.fldNo++
				l.state, l.fstart = Start, at+1
			case b == '\n':
				if err := l.emit(at); err != nil {
					return err
				}
				if err := l.endRec(at); err != nil {
					return err
				}
				l.state, l.fstart = Start, at+1
			case b == '\r':
				l.state, l.crpos = CRPending, at
			default:
				return l.fail(ErrAfterQuote, at)
			}
		case CRPending:
			if b != '\n' {
				return l.fail(ErrLoneCR, l.crpos)
			}
			if err := l.endRec(at); err != nil {
				return err
			}
			l.state, l.fstart = Start, at+1
		}
	}
	return nil
}

// Close 结束流并执行 EOF 收尾。
func (l *Lexer) Close() error {
	if l.dead {
		return l.final
	}
	l.dead = true
	switch l.state {
	case Quoted:
		return l.fail(ErrUnclosed, l.qstart)
	case CRPending:
		return l.fail(ErrLoneCR, l.crpos)
	}
	if l.seen && l.started {
		end := l.abs
		if l.state == QEnd {
			end = l.qepos + 1
		}
		if err := l.emit(end); err != nil {
			return err
		}
		if err := l.endRec(end); err != nil {
			return err
		}
	}
	return nil
}
