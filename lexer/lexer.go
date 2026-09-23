// Package lexer 是可暂停续传的 CSV 字节状态机（RFC 4180 方言）。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 状态
const (
	F = iota // 字段开始
	U        // 未引号字段中
	Q        // 引号字段中
	A        // 引号字段中刚见一个引号
	C        // 行尾 CR 待定
)

// 可判定哨兵错误。
var (
	ErrBareQuote     = errors.New("unexpected quote in unquoted field")
	ErrTrailingQuote = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote = errors.New("unterminated quoted field")
	ErrBareCR        = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLong  = errors.New("field exceeds maximum byte length")
	ErrFieldCount    = errors.New("record exceeds maximum field count")
	ErrRecordCount   = errors.New("record count exceeds limit")
	ErrClosed        = errors.New("lexer already terminated with error")
)

// Limits 为三类上限；0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Handlers 接收词法事件；所有 Cell 偏移均为全局绝对偏移。
type Handlers struct {
	OnField  func(c cell.Cell)
	OnRecord func()
}

// Lexer 逐字节状态机。单实例不是并发安全的。
type Lexer struct {
	state int
	h     Handlers
	lim   Limits

	buf       []byte // 当前字段内容
	quoted    bool   // 当前字段是否出现引号
	start     int    // 当前字段起始偏移
	nbytes    int    // 当前字段已计内容字节数
	fields    int    // 当前记录已发字段数
	records   int    // 已完成记录数
	started   bool   // 流中是否已开始过某条记录
	off       int    // 已消费字节数（下一字节偏移）
	processed int64  // 处理字节总次数
	term      error  // 终态错误

	pendingCR cell.Cell // C 状态挂起字段
	hasCR     bool
}

// New 构造状态机。base 为本流在更大缓冲区中的起始偏移。
func New(h Handlers, lim Limits, base int, state int) *Lexer {
	return &Lexer{h: h, lim: lim, state: state, start: base, off: base}
}

// Processed 返回字节被状态机处理的总次数。
func (l *Lexer) Processed() int64 { return l.processed }

// Err 返回终态错误（nil 表示未终止）。
func (l *Lexer) Err() error { return l.term }

// State 返回当前状态（供 par 定界扫描复用同构逻辑时参考）。
func (l *Lexer) State() int { return l.state }

func (l *Lexer) fail(op string, offset, record, field int, err error) error {
	if l.term == nil {
		l.term = &cell.PosError{Op: op, Offset: offset, Record: record, Field: field, Err: err}
	}
	return l.term
}

// recNo/fieldNo 为下一个出错位置的 1 基编号。
func (l *Lexer) recNo() int   { return l.records + 1 }
func (l *Lexer) fieldNo() int { return l.fields + 1 }

func (l *Lexer) emit(c cell.Cell) error {
	l.fields++
	if l.lim.MaxFields > 0 && l.fields > l.lim.MaxFields {
		return l.fail("field", c.Start, l.recNo(), l.fields, ErrFieldCount)
	}
	l.h.OnField(c)
	return nil
}

func (l *Lexer) endRecord(off int) error {
	l.records++
	if l.lim.MaxRecords > 0 && l.records > l.lim.MaxRecords {
		return l.fail("record", off, l.records, l.fields, ErrRecordCount)
	}
	l.h.OnRecord()
	l.fields = 0
	l.start = off + 1
	l.started = true
	return nil
}

// addByte 计入一个普通内容字节并做字段长度检查。
func (l *Lexer) addByte(b byte, off int) error {
	l.buf = append(l.buf, b)
	l.nbytes++
	if l.lim.MaxFieldBytes > 0 && l.nbytes > l.lim.MaxFieldBytes {
		return l.fail("field", off, l.recNo(), l.fieldNo(), ErrFieldTooLong)
	}
	return nil
}

// Feed 喂入一段字节，可多次调用。
func (l *Lexer) Feed(p []byte) error {
	if l.term != nil {
		return l.term
	}
	for _, b := range p {
		off := l.off
		l.off++
		l.processed++
		if err := l.step(b, off); err != nil {
			return err
		}
	}
	return nil
}

func (l *Lexer) step(b byte, off int) error {
	switch l.state {
	case F:
		switch {
		case b == ',':
			if err := l.emit(cell.Cell{Start: off, End: off}); err != nil {
				return err
			}
		case b == '"':
			l.state, l.quoted = Q, true
		case b == '\n':
			if err := l.emit(cell.Cell{Start: off, End: off}); err != nil {
				return err
			} else if err = l.endRecord(off); err != nil {
				return err
			}
			l.state = F
		case b == '\r':
			l.pendingCR = cell.Cell{Start: off, End: off}
			l.hasCR, l.state = true, C
		default:
			l.buf = l.buf[:0]
			if err := l.addByte(b, off); err != nil {
				return err
			}
			l.state = U
		}
	case U:
		switch {
		case b == ',':
			if err := l.emit(cell.Cell{Value: string(l.buf), Start: l.start, End: off}); err != nil {
				return err
			}
			l.start, l.state = off+1, F
		case b == '"':
			return l.fail("lex", off, l.recNo(), l.fieldNo(), ErrBareQuote)
		case b == '\n':
			if err := l.emit(cell.Cell{Value: string(l.buf), Start: l.start, End: off}); err != nil {
				return err
			} else if err = l.endRecord(off); err != nil {
				return err
			}
			l.state = F
		case b == '\r':
			l.pendingCR = cell.Cell{Value: string(l.buf), Start: l.start, End: off}
			l.hasCR, l.state = true, C
		default:
			if err := l.addByte(b, off); err != nil {
				return err
			}
		}
	case Q:
		if b == '"' {
			l.state = A
		} else {
			if err := l.addByte(b, off); err != nil {
				return err
			}
		}
	case A:
		switch {
		case b == '"':
			l.buf = append(l.buf, '"')
			l.nbytes++
			if l.lim.MaxFieldBytes > 0 && l.nbytes > l.lim.MaxFieldBytes {
				return l.fail("field", off, l.recNo(), l.fieldNo(), ErrFieldTooLong)
			}
			l.state = Q
		case b == ',':
			if err := l.emit(cell.Cell{Value: string(l.buf), Quoted: l.quoted, Start: l.start, End: off + 1}); err != nil {
				return err
			}
			l.start, l.quoted, l.state = off+1, false, F
		case b == '\n':
			if err := l.emit(cell.Cell{Value: string(l.buf), Quoted: l.quoted, Start: l.start, End: off + 1}); err != nil {
				return err
			} else if err = l.endRecord(off); err != nil {
				return err
			}
			l.quoted, l.state = false, F
		default:
			return l.fail("lex", off, l.recNo(), l.fieldNo(), ErrTrailingQuote)
		}
	case C:
		l.hasCR = false
		if b == '\n' {
			c := l.pendingCR
			c.End = off + 1
			if err := l.emit(c); err != nil {
				return err
			} else if err = l.endRecord(off); err != nil {
				return err
			}
			l.state = F
		} else {
			return l.fail("lex", off-1, l.recNo(), l.fieldNo(), ErrBareCR)
		}
	}
	return nil
}

// Close 结束流：发出最后的无换行记录，或报 EOF 处错误。
func (l *Lexer) Close() error {
	if l.term != nil {
		return l.term
	}
	switch l.state {
	case Q:
		return l.fail("lex", l.off, l.recNo(), l.fieldNo(), ErrUnclosedQuote)
	case C:
		return l.fail("lex", l.off-1, l.recNo(), l.fieldNo(), ErrBareCR)
	case U:
		if err := l.emit(cell.Cell{Value: string(l.buf), Start: l.start, End: l.off}); err != nil {
			return err
		}
		if err := l.endRecord(l.off - 1); err != nil {
			return err
		}
	case A:
		if err := l.emit(cell.Cell{Value: string(l.buf), Quoted: true, Start: l.start, End: l.off}); err != nil {
			return err
		}
		if err := l.endRecord(l.off - 1); err != nil {
			return err
		}
	case F:
		// 仅当流中存在过记录（最后一个换行已结算）或有挂起 CR 时才处理；此处无新字段。
		_ = l.started
	}
	return nil
}
