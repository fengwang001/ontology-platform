// Package lexer 是可暂停续传的 CSV 逐字节状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 语法/字段上限哨兵错误。
var (
	ErrQuote          = errors.New("bare field contains quote")
	ErrAfterQuote     = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote  = errors.New("unterminated quoted field")
	ErrBareCR         = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLong   = errors.New("field exceeds max bytes")
)

// Error 带字节偏移（从0起）、记录号与字段号（从1起）。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

// Sink 接收字段与记录事件。返回错误会令状态机进入终态。
type Sink interface {
	Field(cell.Cell) error
	EndRecord() error
}

// 状态枚举。
const (
	SStart = iota // 字段开始
	SBare         // 未引号字段中
	SQuote        // 引号字段中
	SAfterQuote   // 引号字段中刚见到一个引号
	SCR           // 行尾 CR 待定
)

// Lexer 是流式词法器。单实例非并发安全。
type Lexer struct {
	sink      Sink
	maxField  int
	state     int
	pos       int
	recNo     int
	fieldNo   int
	start     int
	val       []byte
	quoted    bool
	opened    bool // 字段是否已开始（用于空行判定）
	flen      int  // 字段内容计数（"" 算 1；\r 行尾计 1）
	closedRec bool // 本记录是否至少发过一个字段
	pending   bool // 有尚未 EndRecord 的字段（区分尾换行/无尾换行）
	finished  bool
	dead      *Error
	bytesRead int // 非导出：状态机处理字节总次数
}

// New 创建词法器。maxFieldBytes<=0 表示不限。
func New(sink Sink, maxFieldBytes int) *Lexer {
	return &Lexer{sink: sink, maxField: maxFieldBytes}
}

// BytesHandled 返回状态机处理的字节总数。
func (l *Lexer) BytesHandled() int { return l.bytesRead }

func (l *Lexer) fail(err error, offset int) error {
	if l.dead == nil {
		l.dead = &Error{Err: err, Offset: offset, Record: l.recNo + 1, Field: l.fieldNo + 1}
	}
	return l.dead
}

// noteByte 登记一个进入字段内容的字节（不含被折叠的第二个引号）。
func (l *Lexer) noteByte() error {
	l.flen++
	if l.maxField > 0 && l.flen > l.maxField {
		return l.fail(ErrFieldTooLong, l.pos)
	}
	return nil
}

func (l *Lexer) openField(quoted bool) {
	l.opened, l.quoted = true, quoted
	l.start = l.pos
}

// emit 发送当前字段。
func (l *Lexer) emit() error {
	l.fieldNo++
	c := cell.Cell{Value: string(l.val), Quoted: l.quoted, Start: l.start, End: l.pos}
	if err := l.sink.Field(c); err != nil {
		return l.fail(err, l.start)
	}
	l.val, l.opened, l.quoted, l.flen = l.val[:0], false, false, 0
	l.closedRec, l.pending = true, true
	return nil
}

func (l *Lexer) endRecord() error {
	if !l.closedRec { // 空行：跳过
		l.pending = false
		return nil
	}
	l.recNo++
	if err := l.sink.EndRecord(); err != nil {
		return l.fail(err, l.start)
	}
	l.fieldNo, l.pending, l.closedRec = 0, false, false
	return nil
}

func (l *Lexer) resetToFieldStart() {
	l.state = SStart
	l.start = l.pos
}

// Feed 喂入一段字节，可调用任意多次。
func (l *Lexer) Feed(p []byte) error {
	if l.dead != nil {
		return l.dead
	}
	for _, b := range p {
		l.bytesRead++
		if err := l.step(b); err != nil {
			return err
		}
		l.pos++
	}
	return nil
}

func (l *Lexer) step(b byte) error {
	switch l.state {
	case SStart:
		switch b {
		case '"':
			l.openField(true)
			l.state = SQuote
		case ',':
			l.openField(false)
			l.val = l.val[:0]
			if err := l.noteByte(); err != nil {
				return err
			}
			if err := l.emit(); err != nil {
				return err
			}
			l.resetToFieldStart()
		case '\r':
			if !l.opened {
				l.state = SCR
			}
		case '\n':
			if l.opened {
				if err := l.emit(); err != nil {
					return err
				}
				if err := l.endRecord(); err != nil {
					return err
				}
			}
			l.resetToFieldStart()
		default:
			l.openField(false)
			l.val = append(l.val, b)
			if err := l.noteByte(); err != nil {
				return err
			}
			l.state = SBare
		}
	case SBare:
		switch b {
		case '"':
			return l.fail(ErrQuote, l.pos)
		case ',':
			if err := l.emit(); err != nil {
				return err
			}
			l.resetToFieldStart()
		case '\r':
			l.flen++
			if l.maxField > 0 && l.flen > l.maxField {
				return l.fail(ErrFieldTooLong, l.pos)
			}
			l.state = SCR
		case '\n':
			if err := l.emit(); err != nil {
				return err
			}
			if err := l.endRecord(); err != nil {
				return err
			}
			l.resetToFieldStart()
		default:
			l.val = append(l.val, b)
			if err := l.noteByte(); err != nil {
				return err
			}
		}
	case SQuote:
		switch b {
		case '"':
			l.state = SAfterQuote
		case '\r', '\n':
			l.val = append(l.val, b)
			if err := l.noteByte(); err != nil {
				return err
			}
		default:
			l.val = append(l.val, b)
			if err := l.noteByte(); err != nil {
				return err
			}
		}
	case SAfterQuote:
		switch b {
		case '"':
			l.val = append(l.val, '"')
			if err := l.noteByte(); err != nil {
				return err
			}
			l.state = SQuote
		case ',':
			if err := l.emit(); err != nil {
				return err
			}
			l.resetToFieldStart()
		case '\r':
			l.flen++
			if l.maxField > 0 && l.flen > l.maxField {
				return l.fail(ErrFieldTooLong, l.pos)
			}
			l.state = SCR
		case '\n':
			if err := l.emit(); err != nil {
				return err
			}
			if err := l.endRecord(); err != nil {
				return err
			}
			l.resetToFieldStart()
		default:
			return l.fail(ErrAfterQuote, l.pos)
		}
	case SCR:
		if b == '\n' {
			if l.opened {
				if err := l.emit(); err != nil {
					return err
				}
				if err := l.endRecord(); err != nil {
					return err
				}
			}
			l.resetToFieldStart()
			return nil
		}
		return l.fail(ErrBareCR, l.pos-1)
	}
	return nil
}

// Close 结束流。
func (l *Lexer) Close() error {
	if l.dead != nil {
		return l.dead
	}
	if l.finished {
		return nil
	}
	l.finished = true
	switch l.state {
	case SQuote, SAfterQuote:
		return l.fail(ErrUnclosedQuote, l.pos)
	case SCR:
		return l.fail(ErrBareCR, l.pos-1)
	case SBare:
		if err := l.emit(); err != nil {
			return err
		}
		return l.endRecord()
	default: // SStart：有悬挂字段（逗号结尾/引号字段已闭合）才发记录
		if l.pending {
			return l.endRecord()
		}
		return nil
	}
}
