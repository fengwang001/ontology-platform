package lexer

import "ontology/cell"

// Handler 接收状态机产出的字段与记录收口事件。
type Handler interface {
	OnCell(c cell.Cell) error
	OnRecord() error
}

// 状态：F 字段起始 / B 未引号中 / Q 引号中 / A 刚见引号 / P CR待定 / E 段内假设（引号内或转义）
const (
	stF = iota
	stB
	stQ
	stA
	stP
	stE
)

// EndState 供 par 判定段末真实状态。
type EndState int

const (
	EndBoundary  EndState = iota // F：边界上，无跨段字段
	EndBare                      // B：未引号字段跨段
	EndQuoted                    // Q：引号字段跨段
	EndAfterQuote                // A：闭合引号刚出现
	EndCR                        // P：\r 待定
)

type machine struct {
	h              Handler
	maxF           int
	st             int
	recOpen        bool
	quoted         bool
	raw            []byte
	start, cr, cls int
	fsz            int
	off            int
	rec, fld       int
	err            error
}

func newMachine(h Handler, off, maxF int, inside bool) *machine {
	m := &machine{h: h, maxF: maxF, off: off, rec: 1, fld: 1, start: off, cr: -1, cls: -1}
	if inside {
		m.st, m.recOpen, m.quoted = stE, true, true
	}
	return m
}

func (m *machine) fail(e error, off int) error {
	m.err = cell.At(e, off, m.rec, m.fld)
	return m.err
}

func (m *machine) grow(b byte) bool {
	m.fsz++
	if m.maxF > 0 && m.fsz > m.maxF {
		m.fail(cell.ErrFieldTooLarge, m.off)
		return false
	}
	m.raw = append(m.raw, b)
	return true
}

func (m *machine) emit(end int) error {
	v := string(m.raw)
	if m.quoted {
		v = Unescape(m.raw)
	}
	err := m.h.OnCell(cell.Cell{Value: v, Quoted: m.quoted, Start: m.start, End: end})
	m.fld++
	return err
}

func (m *machine) boundary(off int) {
	m.start, m.cr, m.cls, m.quoted, m.raw, m.fsz = off+1, -1, -1, false, m.raw[:0], 0
}

func (m *machine) closeRec(cellEnd int) error {
	if err := m.emit(cellEnd); err != nil {
		m.err = err
		return err
	}
	if err := m.h.OnRecord(); err != nil {
		m.err = err
		return err
	}
	m.rec++
	m.fld = 1
	m.recOpen = false
	m.start = m.off + 1
	m.cr, m.cls = -1
	return nil
}

func (m *machine) step(b byte) error {
	m.recOpen = true
	switch m.st {
	case stF:
		switch {
		case b == ',':
			if err := m.emit(m.off); err != nil {
				return err
			}
			m.boundary(m.off)
		case b == '"':
			m.st, m.quoted, m.start = stQ, true, m.off
		case b == '\r':
			m.st, m.cr = stP, m.off
		case b == '\n':
			return m.closeRec(m.off)
		default:
			if !m.grow(b) {
				return m.err
			}
			m.st = stB
		}
	case stB:
		switch {
		case b == ',':
			if err := m.emit(m.off); err != nil {
				return err
			}
			m.boundary(m.off)
			m.st = stF
		case b == '"':
			return m.fail(cell.ErrBadQuote, m.off)
		case b == '\r':
			m.st, m.cr = stP, m.off
		case b == '\n':
			return m.closeRec(m.off)
		default:
			if !m.grow(b) {
				return m.err
			}
		}
	case stQ, stE:
		switch {
		case b == '"' && m.st == stE:
			if !m.grow('"') {
				return m.err
			}
			m.st = stQ
		case b == '"':
			m.st, m.cls = stA, m.off
		default:
			if !m.grow(b) {
				return m.err
			}
			m.st = stQ
		}
	case stA:
		switch {
		case b == ',':
			if err := m.emit(m.cls + 1); err != nil {
				return err
			}
			m.boundary(m.off)
			m.st = stF
		case b == '\r':
			m.st, m.cr = stP, m.off
		case b == '\n':
			return m.closeRec(m.cls + 1)
		case b == '"':
			if !m.grow('"') {
				return m.err
			}
			m.st = stQ
		default:
			return m.fail(cell.ErrAfterQuote, m.off)
		}
	case stP:
		if b != '\n' {
			return m.fail(cell.ErrBareCR, m.cr)
		}
		end := m.cr
		if m.quoted {
			end = m.cls + 1
		}
		if err := m.closeRec(end); err != nil {
			return err
		}
		m.st = stF
	}
	m.off++
	return nil
}

func (m *machine) finish() error {
	if m.err != nil {
		return m.err
	}
	switch m.st {
	case stF:
		if m.recOpen {
			return m.closeRec(m.off)
		}
	case stB:
		return m.closeRec(m.off)
	case stQ, stE:
		return m.fail(cell.ErrUnterminated, m.off)
	case stA:
		return m.closeRec(m.cls + 1)
	case stP:
		return m.fail(cell.ErrBareCR, m.cr)
	}
	return nil
}

func (m *machine) endState() (EndState, int, bool) {
	switch m.st {
	case stB:
		return EndBare, m.start, false
	case stQ, stE:
		return EndQuoted, m.start, true
	case stA:
		return EndAfterQuote, m.start, true
	case stP:
		return EndCR, m.start, m.quoted
	}
	return EndBoundary, m.start, false
}

// Unescape 反转义一个带引号字段的原文（含外围引号），\r\n 原样保留。
func Unescape(raw []byte) string {
	if len(raw) < 2 {
		return ""
	}
	out := make([]byte, 0, len(raw)-2)
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] == '"' && i+1 < len(raw)-1 && raw[i+1] == '"' {
			i++
		}
		out = append(out, raw[i])
	}
	return string(out)
}

// Lexer 是可半包续传的流式解析器；单实例非并发安全。
type Lexer struct {
	m     *machine
	term  error
	bytes int64
}

func New(h Handler, maxFieldBytes int) *Lexer {
	return &Lexer{m: newMachine(h, 0, maxFieldBytes, false)}
}

func (l *Lexer) Feed(p []byte) error {
	if l.term != nil {
		return l.term
	}
	for _, b := range p {
		l.bytes++
		if err := l.m.step(b); err != nil {
			l.term = err
			return err
		}
	}
	return nil
}

func (l *Lexer) Close() error {
	if l.term != nil {
		return l.term
	}
	l.term = l.m.finish()
	return l.term
}

// BytesProcessed 返回状态机处理过的字节总数（每字节恰好一次）。
func (l *Lexer) BytesProcessed() int64 { return l.bytes }

// Sim 是一段缓冲区在某种起始假设下的模拟结果，供 par 使用。
type Sim struct {
	m *machine
	c *Collector
}

// End 返回段末状态、跨段字段起点与是否带引号。
func (s *Sim) End() (EndState, int, bool) { return s.m.endState() }

// Finish 对该模拟执行流结束动作（产出末尾记录或 EOF 错误）。
func (s *Sim) Finish() error { return s.m.finish() }

// Run 从绝对偏移 off0 起模拟 p；inside 为段首是否位于引号字段内。
func Run(p []byte, off0 int, inside bool, maxField int) *Sim {
	c := NewCollector()
	m := newMachine(c, off0, maxField, inside)
	for _, b := range p {
		if err := m.step(b); err != nil {
			break
		}
	}
	return &Sim{m: m, c: c}
}

// Ev 是一个模拟事件。Kind: 1=Cell, 2=记录收口, 3=错误。
type Ev struct {
	Kind       int
	Cell       cell.Cell
	Offset     int
	LRec, LFld int
}

// Collector 收集段内事件（段内坐标：LRec/LFld 从 1 起）。
type Collector struct {
	Evs     []Ev
	rec, fd int
}

func NewCollector() *Collector { return &Collector{rec: 1, fd: 1} }

func (c *Collector) OnCell(cl cell.Cell) error {
	c.Evs = append(c.Evs, Ev{Kind: 1, Cell: cl, Offset: cl.Start, LRec: c.rec, LFld: c.fd})
	c.fd++
	return nil
}

func (c *Collector) OnRecord() error {
	c.Evs = append(c.Evs, Ev{Kind: 2, LRec: c.rec})
	c.rec++
	c.fd = 1
	return nil
}

// ErrEvent 把模拟中发生的错误取为事件坐标形式。
func (s *Sim) ErrEvent() (Ev, bool) {
	if s.m.err == nil {
		return Ev{}, false
	}
	off, rec, fld, _ := cell.Pos(s.m.err)
	return Ev{Kind: 3, Offset: off, LRec: rec, LFld: fld}, true
}
