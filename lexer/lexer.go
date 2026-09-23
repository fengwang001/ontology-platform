// Package lexer 是可暂停、可续传的逐字节 CSV 状态机，依赖 cell。单实例不并发安全。
package lexer

import (
	"strings"

	"ontology/cell"
)

// Limits 为 0 表示不限。
type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }

// stStart 字段开始；stPlain 未引号字段中；stInQ 引号字段中；
// stInQQ 刚见一个引号；stCR 行尾 CR 待定。
const (
	stStart = iota
	stPlain
	stInQ
	stInQQ
	stCR
)

// FEvent 是一次字段产出；Rec/Fld 为段内局部记录号/字段号（par 拼接时平移）。
type FEvent struct {
	Cell     cell.Cell
	Rec, Fld int
}

// Snap 是段结束时可续接的状态。
type Snap struct {
	State                        int
	Val                          string
	Quoted, Started, RecStarted  bool
	FStart, Fields, Recs, LogLen int
}

// Result 是一次段解析结果。Locs 是本段首个字段片段每个逻辑字符的全局偏移。
type Result struct {
	Fields  []FEvent
	RecEnds []int
	Locs    []int
	End     Snap
	Err     *cell.ParseError
	cnt     int64
}

type mach struct {
	lim                                Limits
	st                                 int
	val                                strings.Builder
	quoted, started, recStart, enforce bool
	fStart, nf, nr, logn               int
	cnt                                int64
	err                                *cell.ParseError
	locs                               []int
	firstOn                            bool
	onField                            func(cell.Cell)
	onRec                              func()
}

func (m *mach) fail(k cell.Kind, off int) bool {
	m.err = cell.NewError(k, off, m.nr, m.nf+1)
	return false
}

func (m *mach) beginRec(off int) bool {
	if m.recStart {
		return true
	}
	m.recStart, m.nr, m.nf = true, m.nr+1, 0
	if m.enforce && m.lim.MaxRecords > 0 && m.nr > m.lim.MaxRecords {
		return m.fail(cell.KindTooManyRecords, off)
	}
	return true
}

func (m *mach) add(off int) bool {
	if m.enforce && m.lim.MaxFieldBytes > 0 && m.logn >= m.lim.MaxFieldBytes {
		return m.fail(cell.KindFieldTooLarge, off)
	}
	if m.firstOn {
		m.locs = append(m.locs, off)
	}
	m.logn++
	return true
}

func (m *mach) commaAllowed(off int) bool {
	if m.enforce && m.lim.MaxFields > 0 && m.nf >= m.lim.MaxFields {
		return m.fail(cell.KindTooManyFields, off)
	}
	return true
}

func (m *mach) emit(eol bool, off int) {
	c := cell.Cell{Value: m.val.String(), Quoted: m.quoted, Start: m.fStart, End: off}
	if m.onField != nil {
		m.onField(c)
	}
	m.nf++
	m.val.Reset()
	m.quoted, m.started, m.logn = false, false, 0
	if eol {
		if m.onRec != nil {
			m.onRec()
		}
		m.recStart = false
	}
}

func (m *mach) fail(k cell.Kind, off int) bool { m.err = cell.NewError(k, off, m.nr, m.nf+1); return false }

func (m *mach) beginRec(off int) bool {
	if m.recStart {
		return true
	}
	m.recStart, m.nr, m.nf = true, m.nr+1, 0
	return !(m.enforce && m.lim.MaxRecords > 0 && m.nr > m.lim.MaxRecords) || m.fail(cell.KindTooManyRecords, off)
}

func (m *mach) add(off int) bool {
	if m.enforce && m.lim.MaxFieldBytes > 0 && m.logn >= m.lim.MaxFieldBytes {
		return m.fail(cell.KindFieldTooLarge, off)
	}
	if m.firstOn {
		m.locs = append(m.locs, off)
	}
	m.logn++
	return true
}

func (m *mach) commaAllowed(off int) bool {
	return !(m.enforce && m.lim.MaxFields > 0 && m.nf >= m.lim.MaxFields) || m.fail(cell.KindTooManyFields, off)
}

func (m *mach) emit(eol bool, off int) {
	if m.onField != nil {
		m.onField(cell.Cell{Value: m.val.String(), Quoted: m.quoted, Start: m.fStart, End: off})
	}
	m.nf++
	m.val.Reset()
	m.quoted, m.started, m.logn = false, false, 0
	if eol {
		if m.onRec != nil {
			m.onRec()
		}
		m.recStart = false
	}
}

func (m *mach) step(off int, b byte) bool {
	m.cnt++
	switch m.st {
	case stStart:
		if !m.beginRec(off) {
			return false
		}
		switch b {
		case ',':
			if !m.commaAllowed(off) {
				return false
			}
			m.fStart, m.started = off, true
			m.emit(false, off)
			m.fStart, m.started = off+1, true
		case '"':
			m.fStart, m.quoted, m.started = off, true, true
			m.st = stInQ
		case '\r':
			m.fStart, m.started = off, true
			m.st = stCR
		case '\n':
			if !m.started {
				m.fStart, m.started = off, true
			}
			m.emit(true, off)
			m.st = stStart
		default:
			if !m.add(off) {
				return false
			}
			m.fStart, m.started = off, true
			m.val.WriteByte(b)
			m.st = stPlain
		}
	case stPlain:
		switch b {
		case ',':
			if !m.commaAllowed(off) {
				return false
			}
			m.emit(false, off)
			m.fStart, m.started = off+1, true
			m.st = stStart
		case '"':
			return m.fail(cell.KindQuoteInField, off)
		case '\r':
			m.st = stCR
		case '\n':
			m.emit(true, off)
			m.st = stStart
		default:
			if !m.add(off) {
				return false
			}
			m.val.WriteByte(b)
		}
	case stInQ:
		if b == '"' {
			m.st = stInQQ
		} else {
			if !m.add(off) {
				return false
			}
			m.val.WriteByte(b)
		}
	case stInQQ:
		switch b {
		case ',':
			if !m.commaAllowed(off) {
				return false
			}
			m.emit(false, off)
			m.fStart, m.started = off+1, true
			m.st = stStart
		case '"':
			if !m.add(off) {
				return false
			}
			m.val.WriteByte('"')
			m.st = stInQ
		case '\r':
			if !m.add(off) {
				return false
			}
			m.val.WriteByte('\r')
			m.st = stInQ
		case '\n':
			if !m.add(off) {
				return false
			}
			m.val.WriteByte('\n')
			m.st = stInQ
		default:
			return m.fail(cell.KindCharsAfterQuote, off)
		}
	case stCR:
		if b != '\n' {
			return m.fail(cell.KindBareCR, off-1)
		}
		m.emit(true, off-1)
		m.st = stStart
	}
	return true
}

func (m *mach) snap() Snap {
	return Snap{m.st, m.val.String(), m.quoted, m.started, m.recStart, m.fStart, m.nf, m.nr, m.logn}
}

// Run 在全局偏移 off 起解析 p；inside 表示假设起点在引号内；seed 为起点续接状态（可为零值）。
// Run 只做语法、不强制上限；首个字段片段的逐字符偏移放入 Result.Locs。
func Run(off int, p []byte, inside bool, seed Snap) *Result {
	m := &mach{st: stStart, firstOn: true}
	if inside {
		m.st = stInQ
	}
	r := &Result{Fields: []FEvent{}, RecEnds: []int{}}
	m.onField = func(c cell.Cell) {
		r.Fields = append(r.Fields, FEvent{c, m.nr, m.nf + 1})
		if m.firstOn {
			r.Locs = append([]int(nil), m.locs...)
			m.firstOn = false
		}
	}
	m.onRec = func() { r.RecEnds = append(r.RecEnds, 0) }
	// 注入 seed（段中续接：携带开放值/计数；recStart 由 inside 决定）。
	if seed.State != 0 || seed.RecStarted {
		m.st, m.quoted, m.started, m.recStart = seed.State, seed.Quoted, seed.Started, seed.RecStarted
		m.fStart, m.nf, m.nr, m.logn = seed.FStart, seed.Fields, 0, seed.LogLen
		m.val.WriteString(seed.Val)
	}
	if inside && seed.Val == "" {
		m.quoted, m.started, m.recStart, m.fStart, m.logn = true, true, true, off-1, 1
		m.val.WriteByte('"')
	}
	for i, b := range p {
		if !m.step(off+i, b) {
			break
		}
	}
	if m.firstOn {
		r.Locs = append([]int(nil), m.locs...)
	}
	r.End, r.Err, r.cnt = m.snap(), m.err, m.cnt
	return r
}

// FinishEOF 在末段真实终点 off 判定未闭合引号/孤立 CR；nr/nf 为局部计数。
func FinishEOF(r *Result, off int) error {
	switch r.End.State {
	case stInQ:
		return cell.NewError(cell.KindUnclosedQuote, off, r.End.Recs, r.End.Fields+1)
	case stCR:
		return cell.NewError(cell.KindBareCR, off-1, r.End.Recs, r.End.Fields+1)
	}
	return nil
}

// Count 返回该段状态机处理的字节数。
func (r *Result) Count() int64 { return r.cnt }

// Lexer 是流式解析器；单实例不要求并发安全，多个实例可并行。
type Lexer struct {
	m        mach
	terminal error
	closed   bool
	pos      int
}

// New 构造流式 Lexer。
func New(lim Limits) *Lexer {
	return &Lexer{m: mach{st: stStart, enforce: true, lim: lim}}
}

// OnField / OnRecord 注册回调。
func (l *Lexer) OnField(f func(cell.Cell)) { l.m.onField = f }
func (l *Lexer) OnRecord(f func())         { l.m.onRec = f }

func (l *Lexer) Feed(p []byte) error {
	if l.terminal != nil {
		return l.terminal
	}
	if l.closed {
		return cell.ErrTerminal
	}
	for _, b := range p {
		if !l.m.step(l.pos, b) {
			l.terminal = l.m.err
			return l.terminal
		}
		l.pos++
	}
	return nil
}

func (l *Lexer) Close() error {
	if l.terminal != nil {
		return l.terminal
	}
	if l.closed {
		return cell.ErrTerminal
	}
	l.closed = true
	m := &l.m
	switch m.st {
	case stInQ:
		l.terminal = cell.NewError(cell.KindUnclosedQuote, l.pos, m.nr, m.nf+1)
	case stCR:
		l.terminal = cell.NewError(cell.KindBareCR, l.pos-1, m.nr, m.nf+1)
	default:
		if m.recStart {
			m.emit(true, l.pos)
		}
	}
	return l.terminal
}

// BytesProcessed 返回状态机处理过的字节总数。
func (l *Lexer) BytesProcessed() int64 { return l.m.cnt }
