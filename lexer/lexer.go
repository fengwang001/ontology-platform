// Package lexer 是逐字节、可暂停可续传的 CSV（RFC4180 方言）状态机。
package lexer

import (
	"fmt"

	"ontology/cell"
)

// State 为状态机状态。
type State int

const (
	Clear State = iota // 字段起点
	Bare               // 未引号字段中
	Quoted             // 引号字段中
	QuoteSeen          // 引号字段中刚见一个引号
	CR                 // 行尾 CR 待定
)

// Kind 区分四类语法错误、孤立 CR 与三类上限。
type Kind int

const (
	BareQuote Kind = iota
	QuoteAfterClose
	UnclosedQuote
	LoneCR
	FieldTooLarge
	ColumnMismatch
	TooManyFields
	TooManyRecords
)

// Error 带字节偏移（从0）、记录号、字段号（均从1）。
type Error struct {
	Kind         Kind
	Byte         int
	Record, Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("csv: %s at byte %d record %d field %d",
		names[e.Kind], e.Byte, e.Record, e.Field)
}

// Is 只比较 Kind，使任意位置错误都匹配对应哨兵。
func (e *Error) Is(t error) bool { x, ok := t.(*Error); return ok && x.Kind == e.Kind }

var names = [...]string{"bare quote", "char after closing quote", "unclosed quote",
	"lone carriage return", "field too large", "column count mismatch",
	"too many fields in record", "too many records"}

// Sentinel 用于 errors.Is。
var (
	ErrBareQuote       = &Error{Kind: BareQuote}
	ErrQuoteAfterClose = &Error{Kind: QuoteAfterClose}
	ErrUnclosedQuote   = &Error{Kind: UnclosedQuote}
	ErrLoneCR          = &Error{Kind: LoneCR}
	ErrFieldTooLarge   = &Error{Kind: FieldTooLarge}
	ErrColumnMismatch  = &Error{Kind: ColumnMismatch}
	ErrTooManyFields   = &Error{Kind: TooManyFields}
	ErrTooManyRecords  = &Error{Kind: TooManyRecords}
)

// Limits 为资源上限，0 表示不限。
type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }

// Sink 接收事件；返回错误则状态机立刻中止。Append 字节仅调用期内有效。
type Sink interface {
	BeginField(start int, quoted bool) error
	Append(p []byte) error
	EndField(end int) (cell.Cell, error)
	EndRecord() error
}

// Lexer 为续传状态机（单实例非并发安全）。
type Lexer struct {
	sink                        Sink
	base, n, pos                int
	st                          State
	lim                         Limits
	rec, field, regd            int
	open                        bool
	openStart, openLen          int
	openQuote                   bool
	err                         *Error
}

// New 以给定初态创建；open* 描述跨切点的未闭合字段。
func New(s Sink, base int, st State, oStart, oLen int, quoted bool, lim Limits) *Lexer {
	l := &Lexer{sink: s, base: base, st: st, lim: lim, pos: base,
		open: st == Bare || st == Quoted || st == QuoteSeen,
		openStart: oStart, openLen: oLen, openQuote: quoted}
	if l.open {
		l.regd = 1
		_ = s.BeginField(oStart, quoted)
	}
	return l
}

func (l *Lexer) fail(k Kind, off int) error {
	if l.err == nil {
		l.err = &Error{Kind: k, Byte: off, Record: l.curRec(), Field: l.field + 1}
	}
	return l.err
}

func (l *Lexer) curRec() int {
	if l.rec == 0 {
		return 1
	}
	return l.rec + 1
}

// abort 固化 Sink（组装器）返回的错误。
func (l *Lexer) abort(e error) error {
	if x, ok := e.(*Error); ok {
		if l.err == nil {
			l.err = x
		}
		return l.err
	}
	return l.fail(ColumnMismatch, l.pos)
}

func (l *Lexer) begin(q bool) {
	l.open, l.openStart, l.openLen, l.openQuote, l.regd = true, l.pos, 0, q, 1
	_ = l.sink.BeginField(l.pos, q)
}

// add 追加一个解码字节；append 前检查，超限字节立刻拒绝。
func (l *Lexer) add(b byte) error {
	if l.lim.MaxFieldBytes > 0 && l.openLen >= l.lim.MaxFieldBytes {
		return l.fail(FieldTooLarge, l.pos)
	}
	l.openLen++
	return l.sink.Append([]byte{b})
}

func (l *Lexer) endf() error {
	if !l.open {
		l.begin(false)
	}
	l.open = false
	if _, err := l.sink.EndField(l.pos); err != nil {
		return l.abort(err)
	}
	l.field++
	return nil
}

// endRec：无注册字段的行按空行抑制（跳过）。
func (l *Lexer) endRec() error {
	if l.regd > 0 {
		if err := l.sink.EndRecord(); err != nil {
			return l.abort(err)
		}
		l.rec++
	}
	l.field, l.regd = 0, 0
	return nil
}

// Feed 喂入一段字节，可任意多次调用；终态后返回同一错误。
func (l *Lexer) Feed(p []byte) error {
	if l.err != nil {
		return l.err
	}
	for _, b := range p {
		l.pos = l.base + l.n
		l.n++
		err := l.step(b)
		if err != nil {
			return err
		}
	}
	return nil
}

func (l *Lexer) step(b byte) error {
	switch l.st {
	case Clear:
		switch b {
		case ',':
			return l.endf()
		case '"':
			l.begin(true)
			l.st = Quoted
		case '\r':
			l.st = CR
		case '\n':
			return l.endRec()
		default:
			l.begin(false)
			if err := l.add(b); err != nil {
				return err
			}
			l.st = Bare
		}
	case Bare:
		switch {
		case b == ',':
			l.st = Clear
			return l.endf()
		case b == '\n':
			if err := l.endf(); err != nil {
				return err
			}
			l.st = Clear
			return l.endRec()
		case b == '\r':
			if err := l.endf(); err != nil {
				return err
			}
			l.st = CR
		case b == '"':
			return l.fail(BareQuote, l.pos)
		default:
			if err := l.add(b); err != nil {
				return err
			}
		}
	case Quoted:
		if b == '"' {
			l.st = QuoteSeen
		} else if err := l.add(b); err != nil {
			return err
		}
	case QuoteSeen:
		switch b {
		case '"':
			if err := l.add('"'); err != nil {
				return err
			}
			l.st = Quoted
		case ',':
			l.st = Clear
			return l.endf()
		case '\n':
			if err := l.endf(); err != nil {
				return err
			}
			l.st = Clear
			return l.endRec()
		case '\r':
			if err := l.endf(); err != nil {
				return err
			}
			l.st = CR
		default:
			return l.fail(QuoteAfterClose, l.pos)
		}
	case CR:
		if b == '\n' {
			l.st = Clear
			return l.endRec()
		}
		// 孤立 \r：位置回指该 CR（其记录号是上一条已落定记录号）。
		e := &Error{Kind: LoneCR, Byte: l.pos - 1, Record: l.rec, Field: l.field}
		if l.err == nil {
			l.err = e
		}
		return l.err
	}
	return nil
}

// Close 宣告流结束；非末段（par 中间段）传 false。
func (l *Lexer) Close(final bool) error {
	if l.err != nil {
		return l.err
	}
	switch l.st {
	case Quoted:
		return l.fail(UnclosedQuote, l.pos)
	case CR:
		if l.err == nil {
			l.err = &Error{Kind: LoneCR, Byte: l.pos - 1, Record: l.rec, Field: l.field}
		}
		return l.err
	case Bare, QuoteSeen:
		if err := l.endf(); err != nil {
			return err
		}
		if err := l.endRec(); err != nil {
			return err
		}
	case Clear:
		if l.open {
			if err := l.endf(); err != nil {
				return err
			}
		}
		if err := l.endRec(); err != nil {
			return err
		}
	}
	l.st = Clear
	return nil
}

// N 返回字节被处理总次数。
func (l *Lexer) N() int { return l.n }

// State 返回当前状态。
func (l *Lexer) State() State { return l.st }

// Pos 为下一字节绝对偏移；RecNo/FieldNo 为局部计数。
func (l *Lexer) Pos() int       { return l.pos }
func (l *Lexer) RecNo() int     { return l.rec }
func (l *Lexer) FieldNo() int   { return l.field }
func (l *Lexer) OpenStart() int { return l.openStart }
func (l *Lexer) OpenLen() int   { return l.openLen }
func (l *Lexer) OpenQuote() bool {
	return l.openQuote
}
