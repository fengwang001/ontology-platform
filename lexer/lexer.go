package lexer

import (
	"errors"

	"ontology/cell"
)

var (
	ErrBareQuote     = errors.New("bare '\"' in unquoted field")
	ErrQuoteJunk     = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote = errors.New("unclosed quoted field")
	ErrBareCR        = errors.New("lone '\\r' not followed by '\\n'")
	ErrFieldTooLarge = errors.New("field exceeds max bytes")
	ErrUseAfterClose = errors.New("feed after terminal error")
)

// PosError：Off 字节偏移(0起)；Record/Field 1起，0=不适用。
type PosError struct{ Err error; Off, Record, Field int }

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

type Entry int

const (
	EntFieldStart Entry = iota
	EntUnquoted
	EntQuoted
	EntQuotePending
	EntCRPending
)

type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int } // 0=不限

// Sink 接收流式事件；implicit=空行隐式空字段（应跳过）。
type Sink interface {
	Field(c cell.Cell, implicit bool)
	EndRec(off int)
	EndInput(off int)
}

const (
	TkF byte = iota + 1 // 字段
	TkI                 // 空行隐式空字段
	TkR                 // 记录结束
)

type Tok struct {
	Kind byte
	Cell cell.Cell
	Off  int
}

// Open：段末开放字段。Val 不含跨段前缀；QStart=起点在段外。
type Open struct {
	On, Quoted, QStart bool
	Start, Nb          int
	Val                string
	RecOn              bool
}

type Res struct {
	Toks   []Tok
	Open   Open
	St     Entry
	CRAt   int
	Err    *PosError
	firstQ int
}

func (r Res) FirstQuote() int { return r.firstQ }

type mach struct {
	st              Entry
	val             []byte
	start, nb       int
	quoted, recOn   bool
	pendingF        *cell.Cell // \r 后待 \n 确认的已闭合字段
	toks            []Tok
	err             *PosError
	lim             Limits
	firstQuoteStart int
}

func newMach(st Entry, start int, quoted, recOn bool, lim Limits) *mach {
	return &mach{st: st, start: start, quoted: quoted, recOn: recOn, lim: lim, firstQuoteStart: -1}
}

func (m *mach) fail(e error, off int) bool { m.err = &PosError{Err: e, Off: off}; return false }

func (m *mach) field(k, end int) {
	c := cell.Cell{Value: string(m.val), Quoted: m.quoted, Start: m.start, End: end}
	m.toks = append(m.toks, Tok{Kind: byte(k), Cell: c})
	m.val, m.quoted = nil, false
}

func (m *mach) add(c byte, off int) bool {
	m.val, m.nb = append(m.val, c), m.nb+1
	if m.lim.MaxFieldBytes > 0 && m.nb > m.lim.MaxFieldBytes {
		return m.fail(ErrFieldTooLarge, off)
	}
	return true
}

func (m *mach) step(c byte, off int) bool {
	if m.st == EntCRPending {
		if c != '\n' {
			return m.fail(ErrBareCR, off-1)
		}
		if m.pendingF != nil {
			m.toks = append(m.toks, Tok{Kind: TkF, Cell: *m.pendingF})
			m.pendingF = nil
		}
		m.toks = append(m.toks, Tok{Kind: TkR, Off: off})
		m.st, m.start, m.recOn, m.firstQuoteStart = EntFieldStart, off+1, false, -1
		return true
	}
	switch c {
	case ',':
		m.field(TkF, off)
		m.st, m.start, m.recOn, m.firstQuoteStart = EntFieldStart, off+1, true, -1
	case '"':
		switch {
		case m.st == EntFieldStart:
			m.st, m.quoted, m.start, m.recOn = EntQuoted, true, off, true
			if m.firstQuoteStart < 0 {
				m.firstQuoteStart = off
			}
		case m.st == EntQuoted:
			m.st = EntQuotePending
		default: // EntQuotePending
			if !m.add('"', off) {
				return false
			}
			m.st = EntQuoted
		}
	case '\n':
		if m.recOn {
			m.field(TkF, off)
		} else {
			m.field(TkI, off)
		}
		m.toks = append(m.toks, Tok{Kind: TkR, Off: off})
		m.st, m.start, m.recOn, m.firstQuoteStart = EntFieldStart, off+1, false, -1
	case '\r':
		if m.st == EntQuotePending || (m.st != EntQuoted && m.recOn) || m.st == EntUnquoted {
			// 暂存字段：\n 到来才确认。quotePending 引号已闭合。
			c := cell.Cell{Value: string(m.val), Quoted: m.quoted, Start: m.start, End: off}
			m.pendingF = &c
			m.val = nil
		}
		m.st = EntCRPending
	default:
		if m.st == EntQuotePending {
			return m.fail(ErrQuoteJunk, off)
		}
		if m.st == EntFieldStart {
			m.st = EntUnquoted
		}
		if !m.add(c, off) {
			return false
		}
}
	return true
}

func finish(m *mach, np int) Res {
	r := Res{Toks: m.toks, St: m.st, CRAt: -1, firstQ: m.firstQuoteStart,
		Open: Open{On: m.st != EntFieldStart, Quoted: m.quoted, Start: m.start,
			Nb: m.nb, Val: string(m.val), RecOn: m.recOn},
		Err: m.err}
	if m.st == EntCRPending {
		r.CRAt = np - 1
	}
	return r
}

// RunSeg 一次遍历并行产出三模板：n=fieldStart 外，q=quoted 内，p=quotePending 内。
func RunSeg(p []byte, lim Limits) (n, q, p2 Res, count int) {
	mn := newMach(EntFieldStart, 0, false, false, lim)
	mq := newMach(EntQuoted, -1, true, true, lim)
	mp := newMach(EntQuotePending, -1, true, true, lim)
	for i := 0; i < len(p); i++ {
		if mn.err == nil { mn.step(p[i], i) }
		if mq.err == nil { mq.step(p[i], i) }
		if mp.err == nil { mp.step(p[i], i) }
		if mn.err != nil && mq.err != nil && mp.err != nil { break }
	}
	return finish(mn, len(p)), finish(mq, len(p)), finish(mp, len(p)), 2 * len(p)
}
