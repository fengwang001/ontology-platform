package lexer

import "errors"

var (
	ErrBareQuote     = errors.New("bare quote in unquoted field")
	ErrAfterQuote    = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote = errors.New("unterminated quoted field")
	ErrLoneCR        = errors.New("lone carriage return")
)

// Error 携带字节偏移（从 0）、记录号、字段号（从 1）。
type Error struct {
	Err          error
	Off          int
	Record, Field int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type State int

const (
	StS State = iota // 字段开始
	StU              // 未引号字段中
	StQ              // 引号字段中
	StQQ             // 引号字段中刚见引号
	StCR             // 行尾 CR 待定
)

type Kind int

const (
	KBegin Kind = iota
	KApp
	KCell
	KEndRec
)

// Ev 为状态机事件。Chunk 已解码（"" 还原成 "）；Off/End 为绝对源字节区间。
type Ev struct {
	Kind   Kind
	Chunk  []byte
	Off    int
	End    int
	Quoted bool
}

// Seg 为一段输入在某起始假设下的结果。Lines 为段内行尾数。
type Seg struct {
	St    State
	Evs   []Ev
	Err   *Error
	Lines int
}

type Handler interface {
	Begin(off int, quoted bool)
	Append(b []byte, off, end int)
	Cell(off, end int)
	EndRec()
}

type machine struct {
	st                            State
	pos, line, cellNo, cells      int
	begun, quoted                 bool
	openOff, lastEnd, closeEnd    int
	h                             Handler
}

func errf(st State, e error, off, line, cellNo int, lines int) Seg {
	return Seg{St: st, Err: &Error{Err: e, Off: off, Record: line + 1, Field: cellNo}, Lines: lines}
}

// RunAt 从状态 st、绝对偏移 base 开始处理 p，事件回调 h。
// open=true 表示字段已在段外开始（不发 Begin、cellNo 从 1 计）。
func RunAt(st State, base int, p []byte, open bool, h Handler) Seg {
	m := machine{st: st, pos: base, line: 0, cellNo: 1, begun: open, h: h}
	for i := 0; i < len(p); i++ {
		c, here := p[i], m.pos
		m.pos++
		switch m.st {
		case StS:
			switch c {
			case ',':
				m.cell(here, here)
			case '"':
				m.begun, m.quoted = true, true
				m.openOff = here
				h.Begin(here, true)
				m.st = StQ
			case '\r':
				m.st = StCR
			case '\n':
				m.finishRec(here, here)
			default:
				m.begun, m.openOff = true, here
				h.Begin(here, false)
				m.app(p[i:i+1], here, here+1)
				m.st = StU
			}
		case StU:
			switch c {
			case ',':
				m.cell(m.openOff, here)
				m.begun, m.st = false, StS
			case '"':
				return errf(m.st, ErrBareQuote, here, m.line, m.cellNo, m.line)
			case '\r':
				m.st = StCR
			case '\n':
				m.finishRec(m.openOff, here)
				m.st = StS
			default:
				m.app(p[i:i+1], here, here+1)
			}
		case StQ:
			if c == '"' {
				m.st = StQQ
			} else {
				m.app(p[i:i+1], here, here+1)
			}
		case StQQ:
			switch c {
			case '"':
				m.app([]byte{'"'}, here-1, here+1)
				m.st = StQ
			case ',':
				m.cell(m.openOff, here)
				m.begun, m.st = false, StS
			case '\n':
				m.finishRec(m.openOff, here)
				m.st = StS
			case '\r':
				m.closeEnd = here
				m.st = StCR
			default:
				return errf(m.st, ErrAfterQuote, here, m.line, m.cellNo, m.line)
			}
		case StCR:
			if c == '\n' {
				if m.begun {
					if m.quoted {
						m.finishRec(m.openOff, m.closeEnd)
					} else {
						m.finishRec(m.openOff, here-1)
					}
				} else {
					m.finishRec(here, here)
				}
				m.st = StS
			} else {
				return errf(StCR, ErrLoneCR, here-1, m.line, m.cellNo, m.line)
			}
		}
		}
	}
	return Seg{St: m.st, Lines: m.line}
}

func (m *machine) app(b []byte, off, end int) {
	m.h.Append(b, off, end)
	m.lastEnd = end
}

func (m *machine) cell(off, end int) {
	m.h.Cell(off, end)
	m.cellNo++
}

func (m *machine) finishRec(off, end int) {
	m.h.Cell(off, end)
	m.cellNo = 1
	m.h.EndRec()
	m.line++
	m.begun, m.quoted = false, false
	m.lastEnd, m.closeEnd = 0, 0
}

// Finish 处理 EOF 收口：Q/QQ 未闭合、CR 孤立；否则提交末记录（无尾随换行）。
func Finish(s Seg, endPos int, h Handler) Seg {
	switch s.St {
	case StQ, StQQ:
		s.Err = &Error{Err: ErrUnclosedQuote, Off: endPos, Record: s.Lines + 1, Field: 1}
	case StCR:
		s.Err = &Error{Err: ErrLoneCR, Off: endPos - 1, Record: s.Lines + 1, Field: 1}
	default:
		h.Cell(endPos, endPos)
		h.EndRec()
	}
	return s
}
