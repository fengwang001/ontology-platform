// Package lexer 是 RFC4180 方言的逐字节状态机，支持半包续传与分片重放。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// Options 为词法上限；0 表示不限制。记录数上限由 table 检查。
type Options struct{ MaxFieldBytes, MaxFields int }

// Sink 接收词法事件；坐标一律绝对字节偏移。
type Sink interface {
	Field(c cell.Cell)
	EndRecord(endOffset int) error
}

// State 是状态机显式状态，导出供 par 构造段首快照。
type State uint8

const (
	Start State = iota // 字段起点
	Unquoted
	Quoted
	QuoteSeen // 引号字段刚见闭合引号
	CR        // 未引号侧见到 \r，待定
)

var (
	ErrBareQuote      = errors.New("lexer: bare \" in unquoted field")
	ErrAfterQuote     = errors.New("lexer: unexpected char after closing quote")
	ErrUnclosedQuote  = errors.New("lexer: unclosed quoted field at EOF")
	ErrBareCR         = errors.New("lexer: bare \\r not followed by \\n")
	ErrFieldTooLarge  = errors.New("lexer: field exceeds max bytes")
	ErrTooManyFields  = errors.New("lexer: record exceeds max field count")
	ErrTooManyRecords = errors.New("table: record count exceeds limit")
	ErrColumnCount    = errors.New("table: field count differs from first record")
)

// PosError 携带字节偏移（从 0）、记录号、字段号（均从 1）。
type PosError struct {
	Err           error
	Offset        int
	Record, Field int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// Core 是纯状态机；流式 Lexer 与 par 都复用它。
type Core struct {
	st        State
	open      bool
	val       []byte
	quoted    bool
	startOff  int
	off       int
	rec       int
	fld       int
	opts      Options
	sink      Sink
	bytesSeen int
}

func NewCore(opts Options, sink Sink) *Core { return &Core{opts: opts, sink: sink} }

// Restore 供 par 以段首快照构造模拟。
func Restore(opts Options, sink Sink, s Snapshot) *Core {
	return &Core{opts: opts, sink: sink, st: s.St, open: s.Open,
		val: append([]byte(nil), s.Val...), quoted: s.Quoted,
		startOff: s.StartOff, off: s.Off, rec: s.Rec, fld: s.Fld}
}

func (c *Core) fail(e error, off int) error {
	return &PosError{Err: e, Offset: off, Record: c.rec + 1, Field: c.fld + 1}
}

func (c *Core) begin(quoted bool, pos int) {
	c.open, c.quoted, c.startOff = true, quoted, pos
}

func (c *Core) add(b byte, pos int) error {
	c.val = append(c.val, b)
	if c.opts.MaxFieldBytes > 0 && len(c.val) > c.opts.MaxFieldBytes {
		return c.fail(ErrFieldTooLarge, pos)
	}
	return nil
}

func (c *Core) emit(end int) error {
	c.fld++
	if c.opts.MaxFields > 0 && c.fld > c.opts.MaxFields {
		return c.fail(ErrTooManyFields, end)
	}
	start := c.startOff
	if !c.open {
		start = end
	}
	c.sink.Field(cell.Cell{Value: string(c.val), Quoted: c.quoted,
		Start: start, End: end})
	c.open, c.val, c.quoted = false, nil, false
	return nil
}

func (c *Core) emitRecord(end int) error {
	if e := c.emit(end); e != nil {
		return e
	}
	if e := c.sink.EndRecord(end); e != nil {
		return e
	}
	c.rec++
	c.fld = 0
	c.startOff = c.off
	return nil
}

// Feed 处理一段字节；返回 *PosError 后即终态。
func (c *Core) Feed(p []byte) error {
	for _, b := range p {
		pos := c.off
		c.off++
		c.bytesSeen++
		switch c.st {
		case Start:
			switch b {
			case ',':
				c.begin(false, pos)
				if e := c.emit(pos); e != nil {
					return e
				}
				c.st, c.startOff = Start, c.off
			case '"':
				c.begin(true, pos)
				c.st = Quoted
			case '\n': // 空 LF 行：跳过
			case '\r':
				c.st, c.open = CR, false // 空 CRLF 候选；\n 时不发字段
			default:
				c.begin(false, pos)
				if e := c.add(b, pos); e != nil {
					return e
				}
				c.st = Unquoted
			}
		case Unquoted:
			switch b {
			case ',':
				if e := c.emit(pos); e != nil {
					return e
				}
				c.st, c.startOff = Start, c.off
			case '"':
				return c.fail(ErrBareQuote, pos)
			case '\n':
				if e := c.emitRecord(pos); e != nil {
					return e
				}
				c.st = Start
			case '\r':
				c.st = CR
			default:
				if e := c.add(b, pos); e != nil {
					return e
				}
			}
		case Quoted:
			if b == '"' {
				c.st = QuoteSeen
			} else if e := c.add(b, pos); e != nil {
				return e
			}
		case QuoteSeen:
			switch b {
			case ',':
				if e := c.emit(pos); e != nil {
					return e
				}
				c.st, c.startOff = Start, c.off
			case '"':
				if e := c.add('"', pos); e != nil {
					return e
				}
				c.st = Quoted
			case '\n':
				if e := c.emitRecord(pos); e != nil {
					return e
				}
				c.st = Start
			case '\r':
				return c.fail(ErrBareCR, pos)
			default:
				return c.fail(ErrAfterQuote, pos)
			}
		case CR:
			if b == '\n' {
				if c.open {
					if e := c.emitRecord(pos - 1); e != nil {
						return e
					}
				}
				c.st = Start
			} else {
				return c.fail(ErrBareCR, pos-1)
			}
		}
	}
	return nil
}

// Close 判决 EOF。
func (c *Core) Close() error {
	switch c.st {
	case Quoted:
		return c.fail(ErrUnclosedQuote, c.off)
	case CR:
		return c.fail(ErrBareCR, c.off-1)
	case Start:
		return nil
	default:
		if e := c.emit(c.off); e != nil {
			return e
		}
		return nil
	}
}

// Snapshot 是可跨段传递的最小状态（坐标绝对）。
type Snapshot struct {
	St                      State
	Open                    bool
	Val                     []byte
	Quoted                  bool
	StartOff, Off, Rec, Fld int
}

func (c *Core) Snapshot() Snapshot {
	return Snapshot{St: c.st, Open: c.open, Val: append([]byte(nil), c.val...),
		Quoted: c.quoted, StartOff: c.startOff, Off: c.off, Rec: c.rec, Fld: c.fld}
}

func (c *Core) BytesSeen() int { return c.bytesSeen }

// Lexer 是带终态语义的流式解析器；单实例非并发安全。
type Lexer struct {
	core *Core
	err  error
	done bool
}

func New(opts Options, sink Sink) *Lexer { return &Lexer{core: NewCore(opts, sink)} }

func (l *Lexer) Feed(p []byte) error {
	if l.err != nil {
		return l.err
	}
	if e := l.core.Feed(p); e != nil {
		l.err = e
	}
	return l.err
}

func (l *Lexer) Close() error {
	if l.err != nil || l.done {
		return l.err
	}
	l.done = true
	l.err = l.core.Close()
	return l.err
}

func (l *Lexer) BytesSeen() int { return l.core.BytesSeen() }
