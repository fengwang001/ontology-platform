// Package lexer 是可暂停、可从任意字节偏移续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"
	"fmt"
)

var (
	ErrBareQuote       = errors.New("bare quote in unquoted field")
	ErrQuoteAfterClose = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("unterminated quoted field")
	ErrBareCR          = errors.New("bare carriage return")
	ErrFieldTooLong    = errors.New("field exceeds max bytes")
)

// Error 携带类别、0 起字节偏移、1 起记录号与字段号。
type Error struct {
	Kind          error
	Offset        int
	Record, Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at offset %d (record %d field %d)", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Kind }

// 事件：Open(开字段,Quoted,Start) Data(数据,[Start,End)) Close(End) Record(End)。
const (
	EvOpen = iota
	EvData
	EvClose
	EvRecord
)

type Event struct {
	Kind               int
	Quoted             bool
	Start, End         int
	Data               string
}

const ( // 内部状态
	sFresh = iota
	sBare
	sQuoted
	sQesc
	sCR
)

// Entry/Exit 是一段输入起止处的字段边界状态（供并行切分）。
type Entry int
type Exit int

const (
	EntryFresh Entry = iota
	EntryBare
	EntryQuoted
	EntryQesc
)
const (
	ExitNone Exit = iota
	ExitFresh
	ExitBare
	ExitQuoted
	ExitQesc
	ExitCR
)

type Limits struct{ MaxFieldBytes int }

// Lexer 持有跨 Feed 的全部状态；单个实例非并发安全。
type Lexer struct {
	state                            int
	open, quoted                     bool
	start, fbytes, processed, total  int
	fieldN, recN                     int
	lim                              Limits
	dead                             error
	emit                             func(Event)
}

func New(lim Limits, emit func(Event)) *Lexer { return &Lexer{lim: lim, emit: emit} }

func (l *Lexer) Processed() int { return l.processed }
func (l *Lexer) Dead() error    { return l.dead }

func (l *Lexer) fail(off int, k error) error {
	if l.dead == nil {
		l.dead = &Error{Kind: k, Offset: off, Record: l.recN, Field: l.fieldN}
	}
	return l.dead
}

func (l *Lexer) openField(off int, q bool) {
	l.open, l.quoted, l.start, l.fbytes, l.fieldN = true, q, off, 0, l.fieldN+1
	l.emit(Event{Kind: EvOpen, Quoted: q, Start: off})
}
func (l *Lexer) closeField(end int) { l.open = false; l.emit(Event{Kind: EvClose, End: end}) }
func (l *Lexer) emitRecord(end int) {
	l.recN++
	l.fieldN = 0
	l.emit(Event{Kind: EvRecord, End: end})
}

func (l *Lexer) run(p []byte, off int, pred func(byte) bool) error {
	j := 0
	for j < len(p) && pred(p[j]) {
		j++
	}
	l.fbytes += j
	if l.lim.MaxFieldBytes > 0 && l.fbytes > l.lim.MaxFieldBytes {
		return l.fail(off, ErrFieldTooLong)
	}
	if j > 0 {
		l.emit(Event{Kind: EvData, Start: off, End: off + j, Data: string(p[:j])})
	}
	l.processed += j
	l.total += j
	return nil
}

var bareCont = func(c byte) bool { return c != ',' && c != '"' && c != '\n' && c != '\r' }
var quoteCont = func(c byte) bool { return c != '"' }

// ChunkRun 以入口假设 entry 解析 [base,base+len(p))，返回事件与出口状态。
func ChunkRun(p []byte, base int, entry Entry, lim Limits) ([]Event, Exit, error) {
	var ev []Event
	l := New(lim, func(e Event) { ev = append(ev, e) })
	l.recN = 1
	switch entry {
	case EntryBare:
		l.state, l.open, l.fieldN, l.start = sBare, true, 1, base
	case EntryQuoted:
		l.state, l.open, l.quoted, l.fieldN, l.start = sQuoted, true, true, 1, base
	case EntryQesc:
		l.state, l.open, l.quoted, l.fieldN, l.start = sQesc, true, true, 1, base
	}
	if e := l.Feed(p); e != nil {
		return ev, ExitNone, e
	}
	exit := map[int]Exit{sFresh: ExitFresh, sBare: ExitBare, sQuoted: ExitQuoted, sQesc: ExitQesc, sCR: ExitCR}[l.state]
	return ev, exit, nil
}

// Feed 续传一段输入；Close 前可调用任意多次。
func (l *Lexer) Feed(p []byte) error {
	if l.dead != nil {
		return l.dead
	}
	base := l.total
	for len(p) > 0 {
		off := base
		c := p[0]
		l.processed++
		l.total++
		p = p[1:]
		base++
		switch l.state {
		case sFresh:
			switch c {
			case ',':
				l.openField(off, false)
				l.closeField(off)
			case '"':
				l.openField(off, true)
				l.state = sQuoted
			case '\n':
				l.openField(off, false)
				l.closeField(off)
				l.emitRecord(off + 1)
			case '\r':
				l.openField(off, false)
				l.closeField(off)
				l.state = sCR
			default:
				l.openField(off, false)
				if e := l.dataRun(&p, &base, off, bareCont); e != nil {
					return e
				}
				l.state = sBare
			}
		case sBare:
			switch c {
			case ',':
				l.closeField(off)
				l.state = sFresh
			case '\n':
				l.closeField(off)
				l.emitRecord(off + 1)
				l.state = sFresh
			case '\r':
				l.closeField(off)
				l.state = sCR
			default:
				return l.fail(off, ErrBareQuote)
			}
		case sQuoted:
			if c == '"' {
				l.state = sQesc
				break
			}
			// c 是引号字段内普通字节（含 , \n \r）；把它与后续连续内容一起吐出。
			whole := append([]byte{c}, p...)
			if e := l.dataRun(&whole, new(int), off, quoteCont); e != nil {
				return e
			}
			consumed := l.processed - (off + 1) + 1 // 重新计算见下
			_ = consumed
			// 直接切片推进 p/base 到 run 结束处。
			k := 1
			for k-1 < len(p) && p[k-1] != '"' {
				k++
			}
			// k-1 为 run 长度；推进 p
		}
	}
	return nil
}

func (l *Lexer) dataRun(pp *[]byte, basep *int, off int, pred func(byte) bool) error {
	p := *pp
	j := 0
	for j < len(p) && pred(p[j]) {
		j++
	}
	l.fbytes += j
	if l.lim.MaxFieldBytes > 0 && l.fbytes > l.lim.MaxFieldBytes {
		return l.fail(off, ErrFieldTooLong)
	}
	if j > 0 {
		l.emit(Event{Kind: EvData, Start: off, End: off + j, Data: string(p[:j])})
	}
	l.processed += j
	l.total += j
	*pp = p[j:]
	*basep += j
	return nil
}

// Close 结束流并处理待定状态。
func (l *Lexer) Close() error {
	if l.dead != nil {
		return l.dead
	}
	switch l.state {
	case sQuoted:
		return l.fail(l.start, ErrUnclosedQuote)
	case sCR:
		return l.fail(l.total-1, ErrBareCR)
	case sBare, sQesc:
		l.closeField(l.total)
	l.emitRecord(l.total)
	}
	return nil
}
