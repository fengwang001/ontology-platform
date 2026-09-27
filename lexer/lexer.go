// Package lexer 是可暂停、可续传的逐字节 CSV 状态机。它本身不持有记录概念，
// 只通过 Sink 回调发出「字段片段」与「记录结束」事件；半包续传时开放字段以片段形式挂起。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类语法错误与孤立 CR，彼此可用 errors.Is 区分；另含三类上限错误与终态错误。
var (
	ErrBadQuote        = errors.New("lexer: bare '\"' in unquoted field")
	ErrQuoteAfterClose = errors.New("lexer: unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field at EOF")
	ErrLoneCR          = errors.New("lexer: bare '\\r' not followed by '\\n'")
	ErrFieldTooLarge   = errors.New("lexer: field exceeds max bytes")
	ErrTooManyFields   = errors.New("lexer: record exceeds max fields")
	ErrTooManyRecords  = errors.New("lexer: too many records")
	ErrTerminal        = errors.New("lexer: parser in terminal error state")
)

// PosError 携带字节偏移、物理记录号、字段号（均从 1 起，偏移从 0 起）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d field %d)", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// Event 是一次字段片段或一次记录结束。Kind==1 为字段，Kind==2 为记录结束。
type Event struct {
	Kind int
	Cell cell.Cell
	Skip bool // 该记录为空行（仅结束符，无任何字段），组装时应跳过
}

// Sink 接收事件。
type Sink func(Event)

// Limits 为可配置上限；零值表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields      int
	MaxRecords     int
}

const (
	stStart = iota
	stU
	stInQ
	stQSeen
	stCR
)

// Machine 是状态机。多次 Run 可在任意字节边界续传；End 处理流结束。
type Machine struct {
	state, rec, fld                    int
	open, openQ                        bool
	start, crOff                       int
	val                                []byte
	nbytes                             int
	lim                                Limits
	sink                               Sink
}

// New 创建状态机。
func New(lim Limits, sink Sink) *Machine { return &Machine{state: stStart, sink: sink, lim: lim} }

// Bytes 返回状态机实际处理的输入字节数（合成字节不计）。
func (m *Machine) Bytes() int { return m.nbytes }

func (m *Machine) fail(err error, off int) error {
	return &PosError{Err: err, Offset: off, Record: m.rec + 1, Field: m.fld}
}

func (m *Machine) emitCell(end int) {
	m.sink(Event{Kind: 1, Cell: cell.Cell{Value: string(m.val), Quoted: m.openQ, Start: m.start, End: end}})
	m.val = m.val[:0]
}

// term 发出当前开放字段（若有）与记录结束；newRec 表示其后进入新记录。
func (m *Machine) term(off, end int, skip bool, enforce bool) error {
	if m.open {
		if enforce && m.lim.MaxFieldBytes > 0 && len(m.val) > m.lim.MaxFieldBytes {
			return m.fail(ErrFieldTooLarge, off)
		}
		m.emitCell(end)
		m.open = false
	}
	m.openQ = false
	m.sink(Event{Kind: 2, Skip: skip})
	m.rec++
	m.fld = 0
	m.state = stStart
	if !skip && m.lim.MaxRecords > 0 && m.rec > m.lim.MaxRecords {
		return &PosError{Err: ErrTooManyRecords, Offset: off, Record: m.rec, Field: 1}
	}
	return nil
}

func (m *Machine) beginField(off int, quoted bool) error {
	if m.open {
		if e := m.emitOpen(off); e != nil {
			return e
		}
	}
	m.fld++
	if m.lim.MaxFields > 0 && m.fld > m.lim.MaxFields {
		return m.fail(ErrTooManyFields, off)
	}
	m.open, m.openQ, m.start = true, quoted, off
	return nil
}

func (m *Machine) emitOpen(end int) error {
	if m.lim.MaxFieldBytes > 0 && len(m.val) > m.lim.MaxFieldBytes {
		return m.fail(ErrFieldTooLarge, end)
	}
	m.emitCell(end)
	m.open = false
	m.openQ = false
	return nil
}

// Run 处理 p（绝对偏移从 base 起）。prependQuote 为 true 时假定起点在引号字段内：
// 在首字节前预置一个合成引号（不计字节、不产生错误），用于 par 的「引号内」假设。
func (m *Machine) Run(p []byte, base int, prependQuote bool) error {
	i := 0
	if prependQuote {
		m.state, m.open, m.openQ = stInQ, true, true
		m.start = base
	}
	for i < len(p) {
		b, off := p[i], base+i
		m.nbytes++
		i++
		switch m.state {
		case stStart, stU:
			switch {
			case b == ',':
				if !m.open { // 裸空字段开始并立即结束
					if e := m.beginField(off, false); e != nil {
						return e
					}
				}
				if e := m.emitOpen(off); e != nil {
					return e
				}
			case b == '"':
				if m.state == stU || (m.state == stStart && m.open) {
					return m.fail(ErrBadQuote, off)
				}
				if e := m.beginField(off, true); e != nil {
					return e
				}
				m.state = stInQ
			case b == '\r':
				if !m.open {
					if e := m.beginField(off, false); e != nil {
						return e
					}
				}
				m.state, m.crOff = stCR, off
			case b == '\n':
				if m.state == stStart && !m.open {
					if e := m.term(off, off+1, true, true); e != nil {
						return e
					}
				} else {
					if e := m.term(off, off, false, true); e != nil {
						return e
					}
				}
			default:
				if !m.open {
					if e := m.beginField(off, false); e != nil {
						return e
					}
				}
				m.val = append(m.val, b)
				m.state = stU
			}
		case stInQ:
			if b == '"' {
				m.state = stQSeen
			} else {
				if m.lim.MaxFieldBytes > 0 && len(m.val)+1 > m.lim.MaxFieldBytes {
					return m.fail(ErrFieldTooLarge, off)
				}
				m.val = append(m.val, b)
			}
		case stQSeen:
			switch {
			case b == '"':
				if m.lim.MaxFieldBytes > 0 && len(m.val)+1 > m.lim.MaxFieldBytes {
					return m.fail(ErrFieldTooLarge, off)
				}
				m.val = append(m.val, '"')
				m.state = stInQ
			case b == ',':
				if e := m.emitOpen(off); e != nil {
					return e
				}
			case b == '\n':
				if e := m.term(off, off, false, true); e != nil {
					return e
				}
			default:
				return m.fail(ErrQuoteAfterClose, off)
			}
		case stCR:
			if b == '\n' {
				if e := m.term(off, m.crOff, false, true); e != nil {
					return e
				}
			} else {
				return m.fail(ErrLoneCR, m.crOff)
			}
		}
	}
	return nil
}

// Flush 发出仍开放的字段片段但不终结记录（供 par 在段切处取得片段）。
func (m *Machine) Flush(endOff int) {
	if m.open {
		m.emitCell(endOff)
		m.open = false
	}
}

// OpenKind 报告结束时开放字段类型：0 无开放字段，1 未引号开放，2 引号内，3 CR 待定。
func (m *Machine) OpenKind() int {
	switch {
	case m.state == stCR:
		return 3
	case m.open && m.state == stInQ:
		return 2
	case m.open:
		return 1
	default:
		return 0
	}
}

// CROffset 返回 CR 待定状态下该 CR 的偏移。
func (m *Machine) CROffset() int { return m.crOff }

// End 处理流结束。in 为 true 时为 par 的引号内假设（只冲洗不判未闭合）。
func (m *Machine) End(total int, in bool) error {
	switch m.state {
	case stInQ:
		if in {
			m.Flush(total)
			return nil
		}
		return m.fail(ErrUnclosedQuote, m.start)
	case stCR:
		if e := m.term(total, m.crOff, false, true); e != nil {
			return e
		}
		return m.fail(ErrLoneCR, m.crOff)
	default:
		if m.open {
			if e := m.emitOpen(total); e != nil {
				return e
			}
		}
		if m.open || m.fld > 0 {
			m.sink(Event{Kind: 2})
			m.rec++
		}
		return nil
	}
}
