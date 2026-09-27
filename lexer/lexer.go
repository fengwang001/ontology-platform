// Package lexer 是逐字节、可暂停续传的 CSV 方言词法器，只依赖 cell。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类彼此可区分的语法错误与上限/终态错误。
var (
	ErrQuoteInField    = errors.New("lexer: bare '\"' in unquoted field")
	ErrCharsAfterQuote = errors.New("lexer: unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field at end of input")
	ErrBareCR          = errors.New("lexer: bare carriage return")
	ErrFieldTooLarge   = errors.New("lexer: field exceeds MaxFieldBytes")
	ErrClosed          = errors.New("lexer: feed after close")
)

// Limits 是可配置上限；0 表示不限。
type Limits struct{ MaxFieldBytes int }

// PosError 携带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type PosError struct {
	Err            error
	Offset         int
	Record, Field  int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d field %d)", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// Sink 接收词法事件；返回错误会终止解析并进入终态。
type Sink interface {
	Field(cell.Cell) error
	Record() error
}

const (
	stF  = iota // 字段开始（F/FB 合一，逗号与首字节处理相同）
	stU         // 未引号字段中
	stQ         // 引号字段中
	stQS        // 引号中刚见引号
	stCR        // 行尾 CR 待定
)

// Machine 是流式词法器；单实例非并发安全。
type Machine struct {
	sink        Sink
	maxF        int
	state       int
	buf         []byte
	q           bool
	start       int
	nbytes      int
	rec, fld    int
	seen        bool // 当前记录是否已产出过字段
	started     bool // 当前记录是否已有悬挂字段（含空未引号字段）
	err         *PosError
	done        bool
}

// New 构造词法器。
func New(sink Sink, lim Limits) *Machine {
	return &Machine{sink: sink, maxF: lim.MaxFieldBytes, start: -1}
}

// BytesProcessed 返回字节被状态机处理的总次数。
func (m *Machine) BytesProcessed() int { return m.nbytes }

// Err 返回终态错误。
func (m *Machine) Err() error {
	if m.err == nil {
		return nil
	}
	return m.err
}

func (m *Machine) fail(err error, off int) *PosError {
	return &PosError{Err: err, Offset: off, Record: m.rec + 1, Field: m.fld + 1}
}

func (m *Machine) emit(off int) error {
	st := m.start
	if st < 0 {
		st = off // 空的未引号字段：零宽，位于分隔符处
	}
	c := cell.Cell{Value: m.buf, Quoted: m.q, Start: st, End: off}
	m.buf, m.q, m.start = nil, false, -1
	m.fld++
	m.seen = true
	if err := m.sink.Field(c); err != nil {
		m.err = m.fail(err, st)
		return m.err
	}
	return nil
}

func (m *Machine) endRecord(off int) error {
	if m.emit(off); m.err != nil {
		return m.err
	}
	if err := m.sink.Record(); err != nil {
		m.err = m.fail(err, off)
		return m.err
	}
	m.rec++
	m.fld, m.seen, m.started = 0, false, false
	m.start = -1
	m.state = stF
	return nil
}

// blankLine 处理 F/CR 态下的行尾：空行跳过，否则收尾成记录。
func (m *Machine) blankLine(off int) error {
	if m.seen || m.started {
		return m.endRecord(off)
	}
	m.fld, m.seen, m.started = 0, false, false
	m.start = -1
	m.state = stF
	return nil
}

func (m *Machine) add(b, off byte) error {
	if m.maxF > 0 && len(m.buf)+1 > m.maxF {
		m.err = m.fail(ErrFieldTooLarge, int(off))
		return m.err
	}
	m.buf = append(m.buf, b)
	return nil
}

// Feed 送入一段输入，可调用任意多次。
func (m *Machine) Feed(p []byte) error {
	if m.err != nil {
		return m.err
	}
	if m.done {
		return ErrClosed
	}
	for i := 0; i < len(p); i++ {
		off := m.nbytes
		b := p[i]
		m.nbytes++
		switch m.state {
		case stF:
			switch {
			case b == ',':
				if m.emit(off); m.err != nil { return m.err }; m.started = true
			case b == '"':
				m.q, m.start, m.started, m.state = true, off, true, stQ
			case b == '\n':
				if err := m.blankLine(off); err != nil { return err }
			case b == '\r':
				m.state = stCR
			default:
				m.start, m.started = off, true
				if m.add(b, byte(off)); m.err != nil { return m.err }
				m.state = stU
			}
		case stU:
			switch {
			case b == ',':
				if m.emit(off); m.err != nil { return m.err }; m.started = true
			case b == '"':
				m.err = m.fail(ErrQuoteInField, off)
			case b == '\n':
				if err := m.endRecord(off); err != nil { return err }
			case b == '\r':
				m.state = stCR
			default:
				if m.add(b, byte(off)); m.err != nil { return m.err }
			}
		case stQ:
			if b == '"' {
				m.state = stQS
			} else if m.add(b, byte(off)); m.err != nil {
				return m.err
			}
		case stQS:
			switch {
			case b == ',':
				if m.emit(off); m.err != nil { return m.err }; m.started = true
			case b == '"':
				if m.add('"', byte(off)); m.err != nil { return m.err }
				m.state = stQ
			case b == '\n':
				if err := m.endRecord(off); err != nil { return err }
			case b == '\r':
				m.state = stCR
			default:
				m.err = m.fail(ErrCharsAfterQuote, off)
			}
		case stCR:
			if b == '\n' {
				if err := m.blankLine(off - 1); err != nil { return err }
			} else {
				m.err = m.fail(ErrBareCR, off-1)
			}
		}
		if m.err != nil {
			return m.err
		}
	}
	return nil
}

// Close 结束输入。
func (m *Machine) Close() error {
	if m.err != nil {
		return m.err
	}
	if m.done {
		return ErrClosed
	}
	m.done = true
	switch m.state {
	case stQ:
		m.err = m.fail(ErrUnclosedQuote, m.nbytes)
	case stCR:
		m.err = m.fail(ErrBareCR, m.nbytes-1)
	default:
		if m.seen || m.started {
			if err := m.endRecord(m.nbytes); err != nil {
				return err
			}
		}
	}
	return m.Err()
}
