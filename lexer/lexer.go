// Package lexer 是可暂停/续传的逐字节 CSV（RFC 4180 方言）状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

// Sink 接收词法事件。返回错误会令状态机立刻进入终态。
type Sink interface {
	Field(c cell.Cell) error
	RowEnd(off int) error   // 记录在 off 处因换行闭合
	BlankLine(off int) error // off 处的空行（按设计跳过）
}

// Entry 是一段字节开始时状态机所处的状态（供 par 双假设使用）。
type Entry int

const (
	Start     Entry = iota // 字段开始（引号外）
	InQuoted               // 引号字段中
	QuoteSeen              // 引号字段中刚见闭引号
	CRPending              // 引号外刚见 \r，等下一字节
)

// Limits 为 0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
}

type state int

const (
	stStart state = iota
	stUnquoted
	stQuoted
	stQuoteSeen
	stCR
)

// Machine 是流式状态机；单实例非并发安全。
type Machine struct {
	sink      Sink
	st        state
	off       int // 已消费字节数（全局偏移）
	rec, fld  int // 记录号/字段号（从 1 起）
	val       []byte
	quoted    bool
	started   bool // 当前字段是否已有字节（含开引号）
	recActive bool // 当前记录是否已有字段或字节
	fstart    int
	flen      int // 逻辑字符计数
	lim       Limits
	term      error
	nbytes    int64
}

// New 构造状态机。entry 给出起始状态（半包/分段续传用）。
func New(sink Sink, entry Entry, lim Limits) *Machine {
	m := &Machine{sink: sink, lim: lim, rec: 1, fld: 1}
	switch entry {
	case InQuoted:
		m.st, m.quoted, m.started, m.recActive = stQuoted, true, true, true
	case QuoteSeen:
		m.st, m.quoted, m.started, m.recActive = stQuoteSeen, true, true, true
	case CRPending:
		m.st, m.started, m.recActive = stCR, true, true
	}
	return m
}

// BytesProcessed 返回状态机逐字节处理的总次数。
func (m *Machine) BytesProcessed() int64 { return m.nbytes }

// EntryState 返回当前可跨续传点挂起的入口状态。
func (m *Machine) EntryState() Entry {
	switch m.st {
	case stQuoted:
		return InQuoted
	case stQuoteSeen:
	return QuoteSeen
	case stCR:
		return CRPending
	}
	return Start
}

// Terminal 返回终态错误（nil 表示尚未失败）。
func (m *Machine) Terminal() error { return m.term }

func (m *Machine) fail(kind error, off int) error {
	if m.term == nil {
		m.term = &PosError{Err: kind, Off: off, Record: m.rec, Field: m.fld}
	}
	return m.term
}

func (m *Machine) emitField(end int) error {
	c := cell.Cell{Value: m.val, Quoted: m.quoted, Off: m.fstart, End: end}
	if err := m.sink.Field(c); err != nil {
		m.term = err
		return err
	}
	m.val, m.quoted, m.started, m.flen = nil, false, false, 0
	m.fld++
	m.fstart = m.off
	return nil
}

func (m *Machine) endRow(off int) error {
	if err := m.sink.RowEnd(off); err != nil {
		m.term = err
		return err
	}
	m.rec++
	m.fld = 1
	m.recActive, m.fstart = false, m.off
	return nil
}

func (m *Machine) addByte(b byte, off int) error {
	if m.flen >= m.lim.MaxFieldBytes && m.lim.MaxFieldBytes > 0 {
		return m.fail(ErrFieldTooLarge, off)
	}
	m.flen++
	m.val = append(m.val, b)
	return nil
}

func (m *Machine) beginField(off int) error {
	m.started, m.recActive, m.fstart = true, true, off
	if m.lim.MaxFields > 0 && m.fld > m.lim.MaxFields {
		return m.fail(ErrTooManyFields, off)
	}
	return nil
}

// Feed 追加一段输入。
func (m *Machine) Feed(p []byte) error {
	if m.term != nil {
		return m.term
	}
	for _, b := range p {
		off := m.off
		m.off++
		m.nbytes++
		switch m.st {
		case stStart:
			switch {
			case b == ',':
				if err := m.beginField(off); err != nil {
					return err
				}
				if err := m.emitField(off); err != nil {
					return err
				}
			case b == '"':
				if err := m.beginField(off); err != nil {
					return err
				}
				m.quoted, m.st = true, stQuoted
			case b == '\r':
				m.st = stCR
			case b == '\n':
				if m.recActive {
					if err := m.beginField(off); err != nil {
						return err
					}
					if err := m.emitField(off); err != nil {
						return err
					}
					if err := m.endRow(off); err != nil {
						return err
					}
				} else if err := m.sink.BlankLine(off); err != nil {
					m.term = err
					return err
				}
			default:
				if err := m.beginField(off); err != nil {
					return err
				}
				if err := m.addByte(b, off); err != nil {
					return err
				}
				m.st = stUnquoted
			}
		case stUnquoted:
			switch {
			case b == ',':
				if err := m.emitField(off); err != nil {
					return err
				}
				m.st = stStart
			case b == '"':
				return m.fail(ErrQuoteInBare, off)
			case b == '\r':
				m.st = stCR
			case b == '\n':
				if err := m.emitField(off); err != nil {
					return err
				}
				if err := m.endRow(off); err != nil {
					return err
				}
				m.st = stStart
			default:
				if err := m.addByte(b, off); err != nil {
					return err
				}
			}
		case stQuoted:
			if b == '"' {
				m.st = stQuoteSeen
			} else {
				if err := m.addByte(b, off); err != nil {
					return err
				}
			}
	case stQuoteSeen:
			switch {
			case b == ',':
				if err := m.emitField(off); err != nil {
					return err
				}
				m.st = stStart
			case b == '"':
				if err := m.addByte('"', off); err != nil {
					return err
				}
				m.st = stQuoted
			case b == '\r':
				m.st = stCR
			case b == '\n':
				if err := m.emitField(off); err != nil {
					return err
				}
				if err := m.endRow(off); err != nil {
					return err
				}
				m.st = stStart
			default:
				return m.fail(ErrCharsAfterQuote, off)
			}
		case stCR:
			if b != '\n' {
				return m.fail(ErrBareCR, off-1)
			}
			if !m.started {
				if err := m.beginField(off - 1); err != nil {
					return err
				}
			}
			if err := m.emitField(off - 1); err != nil {
				return err
			}
			if err := m.endRow(off - 1); err != nil {
				return err
			}
			m.st = stStart
		}
	}
	return nil
}

// Close 宣告流结束，处理隐式字段与挂起状态。
func (m *Machine) Close() error {
	if m.term != nil {
		return m.term
	}
	switch m.st {
	case stQuoted:
		return m.fail(ErrUnterminated, m.off)
	case stCR:
		return m.fail(ErrBareCR, m.off-1)
	case stStart:
		if m.started {
			if err := m.emitField(m.off); err != nil {
				return err
			}
			if err := m.endRow(m.off); err != nil {
				return err
			}
		} else if m.recActive {
			if err := m.beginField(m.off); err != nil {
				return err
			}
			if err := m.emitField(m.off); err != nil {
				return err
			}
			if err := m.endRow(m.off); err != nil {
				return err
			}
		}
	default:
		if err := m.emitField(m.off); err != nil {
			return err
		}
		if err := m.endRow(m.off); err != nil {
			return err
		}
	}
	return nil
}

// Err 哨兵。errors.Is 可判定。
var (
	ErrQuoteInBare    = errors.New("lexer: bare quote in unquoted field")
	ErrCharsAfterQuote = errors.New("lexer: unexpected char after closing quote")
	ErrUnterminated   = errors.New("lexer: unterminated quoted field")
	ErrBareCR         = errors.New("lexer: bare carriage return")
	ErrFieldTooLarge  = errors.New("lexer: field exceeds byte limit")
	ErrTooManyFields  = errors.New("lexer: record exceeds field limit")
)

// PosError 携带字节偏移、记录号、字段号（偏移从 0，号从 1）。
type PosError struct {
	Err    error
	Off    int
	Record int
	Field  int
}

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }
