// Package lexer 是可暂停/续传的 CSV 字节状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类语法错误与资源/终态错误，彼此可用 errors.Is 区分。
var (
	ErrBareQuote         = errors.New("lexer: bare '\"' in unquoted field")
	ErrQuoteAfterClose   = errors.New("lexer: unexpected char after closing quote")
	ErrUnterminatedQuote = errors.New("lexer: unterminated quoted field")
	ErrBareCR            = errors.New("lexer: bare '\\r' not followed by '\\n'")
	ErrFieldTooLarge     = errors.New("lexer: field exceeds max bytes")
	ErrTooManyFields     = errors.New("lexer: record exceeds max field count")
	ErrTerminal          = errors.New("lexer: parser in terminal error state")
)

// Limits 是词法层资源上限；零值表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
}

// Error 携带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type Error struct {
	Err    error
	Byte   int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d field %d)", e.Err, e.Byte, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

// Sink 接收词法事件。
type Sink interface {
	Field(cell.Cell)
	EndRecord(pos int)
	Fail(*Error)
}

const (
	stStart = iota // 字段开始
	stUnq          // 未引号字段中
	stQ            // 引号字段中
	stQEnd         // 引号字段中刚见引号
	stCR           // 行尾 CR 待定
)

// Machine 是纯状态机；同一实例非并发安全。
type Machine struct {
	cfg Limits

	state    int
	pos      int
	rec      int
	fieldNo  int
	nFields  int
	curStart int
	curQ     bool
	cur      []byte
	flen     int
	quoteEnd int
	bytes    int

	err *Error
}

// NewMachine 创建状态机。hot=true 表示入口已在引号字段内（par 热假设），
// startPos 是该假设下第一个待处理字节的下标。
func NewMachine(cfg Limits, hot bool, startPos int) *Machine {
	m := &Machine{cfg: cfg, state: stStart, pos: startPos, rec: 1, fieldNo: 1, curStart: startPos, quoteEnd: -1}
	if hot {
		m.state, m.curQ, m.quoteEnd = stQ, true, -1
	}
	return m
}

// Bytes 返回状态机处理过的字节总数（每字节一拍）。
func (m *Machine) Bytes() int { return m.bytes }

// State 返回边界状态类别（par 拼接用）。
func (m *Machine) State() int { return m.state }

// Open 返回未闭合字段的起点、是否引号、已累积内容与编号（par 拼接用）。
func (m *Machine) Open() (start int, quoted bool, v []byte, rec, fieldNo, nFields int) {
	return m.curStart, m.curQ, m.cur, m.rec, m.fieldNo, m.nFields
}

func (m *Machine) fail(kind error, pos int) bool {
	if m.err != nil {
		return false
	}
	m.err = &Error{Err: kind, Byte: pos, Record: m.rec, Field: m.fieldNo}
	m.state = -1
	return true
}

// Err 返回终态错误（无则 nil）。
func (m *Machine) Err() *Error { return m.err }

func (m *Machine) add(b byte, pos int) bool {
	if m.cfg.MaxFieldBytes > 0 && m.flen >= m.cfg.MaxFieldBytes {
		m.fail(ErrFieldTooLarge, pos)
		return false
	}
	m.cur = append(m.cur, b)
	m.flen++
	return true
}

func (m *Machine) closeField(quoteClosed bool, pos int, s Sink) bool {
	end := pos
	if quoteClosed {
		end = m.quoteEnd
	}
	s.Field(cell.Cell{Value: string(m.cur), Quoted: m.curQ, Start: m.curStart, End: end})
	m.nFields++
	m.fieldNo++
	m.cur, m.curQ, m.flen = nil, false, 0
	m.curStart = pos + 1
	m.quoteEnd = -1
	if m.cfg.MaxFields > 0 && m.nFields > m.cfg.MaxFields {
		m.fail(ErrTooManyFields, pos)
		return false
	}
	return true
}

func (m *Machine) endRecord(pos int, s Sink) {
	s.EndRecord(pos)
	m.rec++
	m.fieldNo, m.nFields = 1, 0
	m.curStart = pos + 1
}

// Run 处理一段字节；出错后进入终态。
func (m *Machine) Run(p []byte, s Sink) {
	if m.err != nil {
		s.Fail(m.err)
		return
	}
	for _, b := range p {
		pos := m.pos
		m.pos++
		m.bytes++
		switch m.state {
		case stStart:
			switch b {
			case ',':
				if !m.closeField(false, pos, s) {
					s.Fail(m.err)
					return
				}
			case '\n':
				if m.nFields == 0 { // 空行：跳过
					m.curStart = pos + 1
					break
				}
				if !m.closeField(false, pos, s) {
					s.Fail(m.err)
					return
				}
				m.endRecord(pos, s)
			case '\r':
				m.state = stCR
			case '"':
				m.state, m.curQ, m.curStart, m.quoteEnd = stQ, true, pos, -1
			default:
				if !m.add(b, pos) {
					s.Fail(m.err)
					return
				}
				m.state = stUnq
			}
		case stUnq:
			switch b {
			case ',':
				if !m.closeField(false, pos, s) {
					s.Fail(m.err)
					return
				}
				m.state = stStart
			case '\n':
				if !m.closeField(false, pos, s) {
					s.Fail(m.err)
					return
				}
				m.endRecord(pos, s)
				m.state = stStart
			case '\r':
				m.state = stCR
			case '"':
				m.fail(ErrBareQuote, pos)
				s.Fail(m.err)
				return
			default:
				if !m.add(b, pos) {
					s.Fail(m.err)
					return
				}
			}
		case stQ:
			if b == '"' {
				m.state = stQEnd
			} else {
				if !m.add(b, pos) {
					s.Fail(m.err)
					return
				}
			}
		case stQEnd:
			switch b {
			case ',':
				if !m.closeField(true, pos, s) {
					s.Fail(m.err)
					return
				}
				m.state = stStart
			case '\n':
				if !m.closeField(true, pos, s) {
					s.Fail(m.err)
					return
				}
				m.endRecord(pos, s)
				m.state = stStart
			case '\r':
				m.quoteEnd = pos
				m.state = stCR
			case '"':
				m.quoteEnd = -1
				if !m.add('"', pos) {
					s.Fail(m.err)
					return
				}
				m.state = stQ
			default:
				m.fail(ErrQuoteAfterClose, pos)
				s.Fail(m.err)
				return
			}
		case stCR:
			if m.curQ {
				m.quoteEnd = -1
			}
			switch b {
			case ',':
				if !m.closeField(m.curQ, pos, s) {
					s.Fail(m.err)
					return
				}
				m.state = stStart
			case '\n':
				if !m.closeField(m.curQ, pos-1, s) {
					s.Fail(m.err)
					return
				}
				m.endRecord(pos-1, s)
				m.state = stStart
			default:
				m.fail(ErrBareCR, pos)
				s.Fail(m.err)
				return
			}
		}
	}
}

// Finish 处理流结束：裁决 EOF 相关状态并收尾最后一条记录。
func (m *Machine) Finish(s Sink) {
	if m.err != nil {
		s.Fail(m.err)
		return
	}
	switch m.state {
	case stQ:
		m.fail(ErrUnterminatedQuote, m.pos)
		s.Fail(m.err)
		return
	case stCR:
		m.fail(ErrBareCR, m.pos)
		s.Fail(m.err)
		return
	case stQEnd:
		m.quoteEnd = m.pos
	}
	if m.nFields > 0 || len(m.cur) > 0 || m.curQ {
		m.closeField(m.curQ, m.pos, s)
		if m.err != nil {
			s.Fail(m.err)
			return
		}
		s.EndRecord(m.pos)
	}
}
