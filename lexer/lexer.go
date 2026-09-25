package lexer

import (
	"errors"

	"ontology/cell"
)

// 四类语法错误，彼此可判定、可 errors.Is。
var (
	ErrQuoteInBare     = errors.New("lexer: bare quote in unquoted field")
	ErrCharsAfterQuote = errors.New("lexer: chars after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field")
	ErrBareCR          = errors.New("lexer: bare carriage return")
	ErrFieldTooLarge   = errors.New("lexer: field exceeds max bytes")
)

// FieldEvt 一个字段解析完成；RecEvt 一条记录结束。Off 为全局字节偏移。
type FieldEvt struct{ C cell.Cell }
type RecEvt struct{ Off int }

type state int

const (
	stFieldStart state = iota
	stUnquoted
	stQuoted
	stQuoteSeen
	stCR
)

// Machine 是可暂停续传的逐字节状态机。单实例非并发安全。
type Machine struct {
	St          state
	Off         int // 已消费字节总数（=下一字节的全局偏移）
	Bytes       int // 字节被处理的总次数（测试用）
	MaxField    int // 0 表示不限
	start       int
	buf         []byte
	quoted      bool
	nfield      int // 当前记录已下发字段数
	final       error
	OnField     func(FieldEvt) error
	OnRecord    func(RecEvt) error
}

func (m *Machine) fail(off int, err error) error {
	if m.final == nil {
		m.final = err
	}
	return m.final
}

func (m *Machine) emitField(endOff int) error {
	m.nfield++
	c := cell.Cell{Value: m.buf, Quoted: m.quoted, Start: m.start, End: endOff}
	m.buf = nil
	m.quoted = false
	if m.OnField != nil {
		if err := m.OnField(FieldEvt{C: c}); err != nil {
			return m.fail(endOff, err)
		}
	}
	return nil
}

// Feed 送入一段字节；可调用任意次，切点可落在任意字节之间。
func (m *Machine) Feed(p []byte) error {
	if m.final != nil {
		return m.final
	}
	for _, b := range p {
		m.Bytes++
		off := m.Off
		m.Off++
		switch m.St {
		case stFieldStart:
			m.start = off
			switch b {
			case ',':
				if err := m.emitField(off); err != nil { return err }
			case '"':
				m.quoted = true
				m.St = stQuoted
			case '\n':
				if err := m.emitField(off); err != nil { return err }
				if err := m.onRecord(off); err != nil { return err }
			case '\r':
				m.St = stCR
			default:
				if err := m.append(b); err != nil { return m.fail(off, err) }
				m.St = stUnquoted
			}
		case stUnquoted:
			switch b {
			case ',':
				if err := m.emitField(off); err != nil { return err }
				m.St = stFieldStart
			case '\n':
				if err := m.emitField(off); err != nil { return err }
				if err := m.onRecord(off); err != nil { return err }
			case '\r':
				m.St = stCR
			case '"':
				return m.fail(off, ErrQuoteInBare)
			default:
				if err := m.append(b); err != nil { return m.fail(off, err) }
			}
		case stQuoted:
			switch b {
			case '"':
				m.St = stQuoteSeen
			default:
				if err := m.append(b); err != nil { return m.fail(off, err) }
			}
		case stQuoteSeen:
			switch b {
			case '"':
				if err := m.append('"'); err != nil { return m.fail(off, err) }
				m.St = stQuoted
			case ',':
				if err := m.emitField(off); err != nil { return err }
				m.St = stFieldStart
			case '\n':
				if err := m.emitField(off); err != nil { return err }
				if err := m.onRecord(off); err != nil { return err }
			case '\r':
				m.St = stCR
			default:
				return m.fail(off, ErrCharsAfterQuote)
			}
		case stCR:
			m.St = stFieldStart
			if b == '\n' {
				if err := m.emitField(off - 1); err != nil { return err }
				if err := m.onRecord(off); err != nil { return err }
			} else {
				return m.fail(off-1, ErrBareCR)
			}
		}
	}
	return nil
}

func (m *Machine) append(b byte) error {
	if m.MaxField > 0 && len(m.buf) >= m.MaxField {
		return ErrFieldTooLarge
	}
	m.buf = append(m.buf, b)
	return nil
}

func (m *Machine) onRecord(off int) error {
	m.nfield = 0
	if m.OnRecord != nil {
		if err := m.OnRecord(RecEvt{Off: off}); err != nil {
			return m.fail(off, err)
		}
	}
	return nil
}

// Close 结束流，冲刷最后一条无换行结尾的记录。
func (m *Machine) Close() error {
	if m.final != nil {
		return m.final
	}
	switch m.St {
	case stQuoted:
		return m.fail(m.Off, ErrUnclosedQuote)
	case stCR:
		return m.fail(m.Off-1, ErrBareCR)
	}
	// stQuoteSeen：引号已闭合但终止符未到，字段仍待下发。
	// stFieldStart 且已有字段：逗号挂起的空尾字段；否则是终止符之后，无挂起记录。
	pending := m.St == stUnquoted || m.St == stQuoteSeen || (m.St == stFieldStart && m.nfield > 0)
	if !pending {
		return nil
	}
	if err := m.emitField(m.Off); err != nil {
		return err
	}
	return m.onRecord(m.Off)
}
