// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类语法错误与三类上限错误，彼此可用 errors.Is 判定。
var (
	ErrBareQuote       = errors.New("lexer: unquoted field contains a quote")
	ErrTrailingGarbage = errors.New("lexer: characters after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field at end of input")
	ErrLoneCR          = errors.New("lexer: bare carriage return not followed by newline")
	ErrFieldTooLarge   = errors.New("lexer: field exceeds maximum size")
	ErrTooManyFields   = errors.New("lexer: record exceeds maximum field count")
	ErrTooManyRecords  = errors.New("lexer: input exceeds maximum record count")
)

// Config 是可配置上限；0 表示不限制。
type Config struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Error 带出错位置（字节偏移从 0 起；记录号、字段号从 1 起）。
type Error struct {
	Err    error
	Byte   int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Byte, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

// Emit 事件：每结束一个字段调用一次；RecordEnd 标记一条记录结束；Blank 标记空行（无字段）。
type Emit func(c cell.Cell, recordEnd, blank bool)

// Lexer 是增量 CSV 状态机。单个实例非并发安全。
type Lexer struct {
	cfg    Config
	emit   Emit
	state  state
	buf    []byte // 当前字段解码后内容
	start  int    // 当前字段起始字节偏移
	qpos   int    // 开引号偏移（引号字段）
	recNo  int    // 已结束的非空行记录数
	fields int    // 当前记录已结束字段数
	dirty  bool   // 当前记录是否已有内容/字段
	off    int    // 已消费字节数
	crOff  int    // 待定 CR 的偏移
	bytes  int64  // 字节处理计数
	err    *Error
}

type state int

const (
	stFS state = iota // 字段开始
	stU               // 未引号字段中
	stQ               // 引号字段中
	stQC              // 引号字段中刚见到一个引号
	stCR              // 行尾 CR 待定
)

// New 创建增量解析器，字段事件通过 emit 回调送出。
func New(cfg Config, emit Emit) *Lexer { return &Lexer{crOff: -1, emit: emit, cfg: cfg} }

// BytesProcessed 返回字节被状态机处理的总次数。
func (l *Lexer) BytesProcessed() int64 { return l.bytes }

// State 是状态机当前所处状态。
type State int

const (
	StateFS State = State(stFS)
	StateU  State = State(stU)
	StateQ  State = State(stQ)
	StateQC State = State(stQC)
	StateCR State = State(stCR)
)

// Snapshot 返回段末状态、当前未结束字段（无则 ok=false）、待决 CR 偏移。
// 供并行拼接在段间合并被劈开的字段，不推进状态机。
func (l *Lexer) Snapshot() (st State, pending *cell.Cell, crOff int) {
	var pc *cell.Cell
	switch {
	case l.state == stQ || l.state == stQC:
		pc = &cell.Cell{Value: string(l.buf), Quoted: true, Start: l.qpos, End: l.off}
	case l.state == stU || l.state == stCR:
		pc = &cell.Cell{Value: string(l.buf), Start: l.start, End: l.off}
	case l.state == stFS && l.dirty:
		pc = &cell.Cell{Start: l.start, End: l.off}
	}
	return State(l.state), pc, l.crOff
}

func (l *Lexer) fail(err error, off int) *Error {
	e := &Error{Err: err, Byte: off, Record: l.recNo + 1, Field: l.fields + 1}
	l.err = e
	return e
}

func (l *Lexer) emitCell(end int) {
	c := cell.Cell{Value: string(l.buf), Start: l.start, End: end}
	if l.qpos >= 0 {
		c.Quoted = true
		c.Start = l.qpos
	}
	l.emit(c, false, false)
	l.buf = l.buf[:0]
	l.qpos = -1
	l.fields++
}

func (l *Lexer) endRecord(end int, blank bool) {
	if !blank {
		l.emitCell(end)
		l.emit(cell.Cell{}, true, false)
		l.recNo++
	} else {
		l.emit(cell.Cell{}, false, true)
	}
	l.fields, l.dirty = 0, false
}

func (l *Lexer) add(b byte, off int) error {
	if l.cfg.MaxFieldBytes > 0 && len(l.buf) >= l.cfg.MaxFieldBytes {
		return l.fail(ErrFieldTooLarge, off)
	}
	l.buf = append(l.buf, b)
	return nil
}

func (l *Lexer) beginRecord(off int) error {
	if !l.dirty {
		if l.cfg.MaxRecords > 0 && l.recNo >= l.cfg.MaxRecords {
			return l.fail(ErrTooManyRecords, off)
		}
		l.dirty = true
	}
	return nil
}

func (l *Lexer) checkFields(off int) error {
	if l.cfg.MaxFields > 0 && l.fields+1 > l.cfg.MaxFields {
		return l.fail(ErrTooManyFields, off)
	}
	return nil
}

// Feed 喂入一段字节，可调用任意多次。
func (l *Lexer) Feed(p []byte) error {
	if l.err != nil {
		return l.err
	}
	for _, b := range p {
		off := l.off
		l.off++
		l.bytes++
		switch l.state {
		case stFS:
			l.start = off
			l.qpos = -1
			switch b {
			case ',':
				if err := l.beginRecord(off); err != nil {
					return err
				}
				if err := l.checkFields(off); err != nil {
					return err
				}
				l.emitCell(off)
			case '"':
				if err := l.beginRecord(off); err != nil {
					return err
				}
				l.qpos, l.state = off, stQ
			case '\n':
				if l.dirty {
					if err := l.checkFields(off); err != nil {
						return err
					}
					l.endRecord(off, false)
				} else {
					l.emit(cell.Cell{}, false, true)
				}
			case '\r':
				l.crOff, l.state = off, stCR
			default:
				if err := l.beginRecord(off); err != nil {
					return err
				}
				if err := l.add(b, off); err != nil {
					return err
				}
				l.state = stU
			}
		case stU:
			switch b {
			case ',':
				if err := l.checkFields(off); err != nil {
					return err
				}
				l.emitCell(off)
				l.state = stFS
			case '"':
				return l.fail(ErrBareQuote, off)
			case '\n':
				l.endRecord(off, false)
				l.state = stFS
			case '\r':
				l.crOff, l.state = off, stCR
			default:
				if err := l.add(b, off); err != nil {
					return err
				}
			}
		case stQ:
			if b == '"' {
				l.state = stQC
			} else {
				if err := l.add(b, off); err != nil {
					return err
				}
			}
		case stQC:
			switch b {
			case ',':
				if err := l.checkFields(off); err != nil {
					return err
				}
				l.emitCell(off)
				l.state = stFS
			case '"':
				if err := l.add('"', off); err != nil {
					return err
				}
				l.state = stQ
			case '\n':
				l.endRecord(off, false)
				l.state = stFS
			default:
				return l.fail(ErrTrailingGarbage, off)
			}
		case stCR:
			if b != '\n' {
				return l.fail(ErrLoneCR, l.crOff)
			}
			if l.dirty {
				l.endRecord(l.crOff, false)
			} else {
				l.emit(cell.Cell{}, false, true)
			}
			l.state = stFS
		}
	}
	return nil
}

// Close 结束流：处理 EOF 语义。
func (l *Lexer) Close() error {
	if l.err != nil {
		return l.err
	}
	switch l.state {
	case stCR:
		return l.fail(ErrLoneCR, l.crOff)
	case stQ:
		return l.fail(ErrUnclosedQuote, l.off)
	case stU, stQC:
		if err := l.checkFields(l.off); err != nil {
			return err
		}
		l.endRecord(l.off, false)
	case stFS:
		// 净 FS：无内容；脏 FS 只能由逗号造成，此时最后字段为空待提交。
		if l.dirty {
			if err := l.checkFields(l.off); err != nil {
				return err
			}
			l.endRecord(l.off, false)
		}
	}
	return nil
}
