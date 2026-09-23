// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类语法错误哨兵。
var (
	ErrQuoteInBare   = errType("unexpected quote in unquoted field")
	ErrBareQuote     = errType("unexpected character after closing quote")
	ErrUnclosedQuote = errType("unterminated quoted field")
	ErrBareCR        = errType("bare carriage return not followed by newline")
	ErrFieldTooLarge = errType("field exceeds maximum byte length")
	ErrTooManyFields = errType("record exceeds maximum field count")
	ErrTooManyRecords = errType("input exceeds maximum record count")
)

type errType string

func (e errType) Error() string { return string(e) }

// PosError 携带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%s at byte %d (record %d, field %d)", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// Limits 是可配置的资源上限；零值表示不限制。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Event 是状态机产生的字节级事件，供 table 与 par 消费。
type Event struct {
	Kind  byte
	C     byte
	Quoted bool
	Start int // start: 字段起点；record: 行尾偏移
	End   int // cell: 字段结束偏移（半开区间）
}

// 事件种类。
const (
	// EvStart 表示一个有内容字段的开始（Start=起点, Quoted=引号标记）。
	EvStart byte = iota
	// EvCont 向当前悬置字段追加一个输出字节 C。
	evCont
	// EvCell 结束当前字段；Start/End 为半开字节区间，Quoted 为引号标记。
	EvCell
	// EvRecord 结束当前记录；Start 为行尾字节偏移（EOF 收尾时为末字节后一位）。
	EvRecord
)

// Entry 是分段解析时段首的进入方式。
type Entry int

const (
	// EntryFresh：记录/字段边界（S 态）。
	EntryFresh Entry = iota
	// EntryBare：段首落在未引号字段内部（U 态）。
	EntryBare
	// EntryQuoted：段首落在引号字段内部（Q 态）。
	EntryQuoted
)

// Snapshot 是状态机在某一时刻的出口状态，供 par 选择下一段假设。
type Snapshot struct {
	Entry     Entry // 下一段若从此点切分应使用的进入方式
	Record    int   // 已开始的记录号（从 1 起）
	Field     int   // 当前字段号（从 1 起）
	FieldLen  int   // 当前悬置字段已累计的输出字节数
	FieldStart int  // 当前悬置字段起点
	InRecord  bool  // 是否有一条记录已开始但尚未发出 EvRecord
}

// Lexer 是单线程流式状态机；单个实例不要求并发安全。
type Lexer struct {
	emit   func(Event) error
	lim    Limits
	state  byte
	off    int
	recNo  int
	field    int // 当前记录内已开始字段号；0 表示记录边界
	recStart bool
	flen   int
	fstart int
	quoted bool
	open   bool // 当前字段已开始且尚未 EvCell
	crOff  int
	dead   error
	nbytes int
}

const (
	stS byte = iota
	stU
	stQ
	stP
	stC
)

// New 创建从输入起点开始的流式状态机。ev 接收字段/记录事件。
func New(lim Limits, ev func(Event) error) *Lexer {
	return NewAt(lim, ev, 0, EntryFresh, 0, 0, 0)
}

// NewAt 创建带全局基址与段首假设的状态机，供 par 使用。
// baseOff 为本段首字节全局偏移；recNo/fieldNo 为编号（字段内续传时 fieldNo>0）。
func NewAt(lim Limits, ev func(Event) error, baseOff int, e Entry, recNo, fno, flen int, fstart int) *Lexer {
	l := &Lexer{emit: ev, lim: lim, off: baseOff, recNo: recNo, field: fno, flen: flen, fstart: fstart, state: stS, recStart: fno == 0}
	switch e {
	case EntryBare:
		l.state, l.open, l.quoted, l.recStart = stU, true, false, false
	case EntryQuoted:
		l.state, l.open, l.quoted, l.recStart = stQ, true, true, false
	}
	return l
}

func (l *Lexer) fail(err error, off int) error {
	if l.dead == nil {
		l.dead = &PosError{Err: err, Offset: off, Record: l.recNo, Field: l.field}
	}
	return l.dead
}

// beginField 在 S 态收到任意字节时编号一个新字段、计数新记录。
func (l *Lexer) beginField() error {
	if l.recStart {
		l.recNo++
		if l.lim.MaxRecords > 0 && l.recNo > l.lim.MaxRecords {
			return l.fail(ErrTooManyRecords, l.off)
		}
		l.field = 1
		l.recStart = false
	} else {
		l.field++
		if l.lim.MaxFields > 0 && l.field > l.lim.MaxFields {
			return l.fail(ErrTooManyFields, l.off)
			}
	}
	l.flen = 0
	l.open = true
	return nil
}

func (l *Lexer) addOut(b byte) error {
	if l.lim.MaxFieldBytes > 0 && l.flen >= l.lim.MaxFieldBytes {
		return l.fail(ErrFieldTooLarge, l.off)
	}
	l.flen++
	return l.emit(Event{Kind: evCont, C: b})
}

func (l *Lexer) cell(end int) error {
	c := Event{Kind: EvCell, Start: l.fstart, End: end, Quoted: l.quoted}
	if !l.open {
		c.Start, c.End = end, end
	}
	l.open = false
	return l.emit(c)
}

func (l *Lexer) endRecord(off int) error {
	if err := l.cell(off); err != nil {
		return err
	}
	l.recNo++
	l.field, l.flen, l.recStart = 0, 0, true
	return l.emit(Event{Kind: EvRecord, Start: off})
}

// Feed 送入一段字节，可调用任意次。
func (l *Lexer) Feed(p []byte) error {
	for i := 0; i < len(p); i++ {
		if l.dead != nil {
			return l.dead
		}
		b := p[i]
		l.nbytes++
		off := l.off
		l.off++
		switch l.state {
		case stS:
			if err := l.beginField(); err != nil {
				return err
			}
			l.fstart = off
			switch b {
			case ',':
				if err := l.cell(off); err != nil {
					return err
				}
			case '"':
				l.state, l.quoted = stQ, true
				if err := l.emit(Event{Kind: EvStart, Start: off, Quoted: true}); err != nil {
					return err
				}
			case '\n':
				if err := l.endRecord(off); err != nil {
					return err
				}
			case '\r':
				l.state, l.crOff = stC, off
			default:
				l.state = stU
				if err := l.emit(Event{Kind: EvStart, Start: off, Quoted: false}); err != nil {
					return err
				}
				if err := l.addOut(b); err != nil {
					return err
				}
			}
		case stU:
			switch b {
			case ',':
				if err := l.cell(off); err != nil {
					return err
				}
				l.state = stS
			case '\n':
				if err := l.endRecord(off); err != nil {
					return err
				}
				l.state = stS
			case '\r':
				l.state, l.crOff = stC, off
			case '"':
				return l.fail(ErrQuoteInBare, off)
			default:
				if err := l.addOut(b); err != nil {
					return err
				}
			}
		case stQ:
			switch b {
			case '"':
				l.state = stP
			default:
				if err := l.addOut(b); err != nil {
					return err
				}
			}
		case stP:
			switch b {
			case '"':
				l.state = stQ
				if err := l.addOut('"'); err != nil {
					return err
				}
			case ',':
				if err := l.cell(off); err != nil {
					return err
				}
				l.state = stS
			case '\n':
				if err := l.endRecord(off); err != nil {
					return err
				}
				l.state = stS
			default:
				return l.fail(ErrBareQuote, off)
			}
		case stC:
			switch b {
			case '\n':
				// CR 属于行尾（\r\n），字段不含 CR。
				if err := l.endRecord(off - 1); err != nil {
					return err
				}
				l.state = stS
			default:
				return l.fail(ErrBareCR, l.crOff)
			}
		}
	}
	return nil
}

// Feed 送入一段字节，可调用任意次。
func (l *Lexer) Feed(p []byte) error { return nil }

// Close 结束输入。
func (l *Lexer) Close() error { return nil }

// BytesProcessed 返回状态机处理过的字节总数（非导出计数的读口）。
func (l *Lexer) BytesProcessed() int { return 0 }
