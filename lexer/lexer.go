// Package lexer 是逐字节 CSV(RFC4180 方言) 状态机，可暂停续传，
// 也提供无状态的段扫描 ScanSegment 供 par 双假设并行使用。
package lexer

import (
	"errors"
	"strings"

	"ontology/cell"
)

// 四类语法错误 + 字段过长，彼此可用 errors.Is 区分。
var (
	ErrBareQuote     = errors.New("bare '\"' in unquoted field")
	ErrAfterQuote    = errors.New("unexpected byte after closing quote")
	ErrUnclosedQuote = errors.New("unterminated quoted field at end of input")
	ErrOrphanCR      = errors.New("lone '\\r' not followed by '\\n'")
	ErrFieldTooLong  = errors.New("field exceeds MaxFieldBytes")
)

// Error 携带字节偏移(从0)、记录号、字段号(从1)。
type Error struct {
	Kind          error
	Offset        int
	Record, Field int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

// Sink 接收词法事件；返回错误会立即终结状态机。
type Sink interface {
	OnField(c cell.Cell) error
	OnEndRecord() error
}

// Entry 是段扫描的入场状态（由上一段出口决定）。
type Entry int

const (
	EntryFS Entry = iota // 新字段开始（覆盖 FS）
	EntryU               // 未引号字段续传
	EntryQ               // 引号字段续传
	EntryQS              // 引号续传，且首字节视为闭合引号后
)

// Ev 是段内词法事件，坐标为段内局部坐标。
type Ev struct {
	Field bool // true=OnField(C)；false=OnEndRecord
	C     cell.Cell
}

// SegResult 是一段扫描结果。
type SegResult struct {
	Evs       []Ev
	Exit      Entry
	CRPending bool // 段末停在未引号 CR 待定
	CRBlank   bool // 该 CR 属于尚未开始的空行
	Pending   cell.Cell
	HasPend   bool
	Err       *Error
}

const (
	stFS = iota
	stU
	stQ
	stQS
	stCR
)

type core struct {
	st                            int
	off, rec, fld, fstart, qclose int
	started, quoted               bool
	crBlank                       bool
	val                           strings.Builder
	nval                          int
	maxField                      int
}

func newCore(entry Entry, maxField int) *core {
	m := &core{st: stFS, rec: 1, fld: 1, maxField: maxField}
	switch entry {
	case EntryU:
		m.st, m.started = stU, true
	case EntryQ:
		m.st, m.started, m.quoted = stQ, true, true
	case EntryQS:
		m.st, m.started, m.quoted = stQS, true, true
	}
	return m
}

func (m *core) content(b byte, off int, emit func(cell.Cell) error) error {
	m.val.WriteByte(b)
	m.nval++
	if m.maxField > 0 && m.nval > m.maxField {
		return &Error{Kind: ErrFieldTooLong, Offset: off, Record: m.rec, Field: m.fld}
	}
	return nil
}

func (m *core) emitField(emit func(cell.Cell) error) error {
	c := cell.Cell{Value: m.val.String(), Quoted: m.quoted, Start: m.fstart, End: m.off}
	if m.quoted {
		c.End = m.qclose
	}
	if err := emit(c); err != nil {
		return m.tagOff(err)
	}
	m.fld++
	m.val.Reset()
	m.nval, m.quoted = 0, false
	m.fstart = m.off
	m.started = true
	return nil
}

func (m *core) tagOff(err error) error {
	var e *Error
	if errors.As(err, &e) && e.Offset < 0 {
		e.Offset = m.off
	}
	return err
}

func (m *core) endRec(emit func(cell.Cell) error, rec func() error) error {
	if err := m.emitField(emit); err != nil {
		return err
	}
	if err := m.tagOff(rec()); err != nil {
		return err
	}
	m.rec++
	m.fld = 1
	m.started = false
	m.fstart = m.off
	return nil
}

func (m *core) fail(k error) error {
	return &Error{Kind: k, Offset: m.off, Record: m.rec, Field: m.fld}
}

func (m *core) step(b byte, emit func(cell.Cell) error, rec func() error) error {
	switch m.st {
	case stFS:
		switch {
		case b == ',':
			return m.emitField(emit)
		case b == '"':
			m.st, m.quoted, m.started = stQ, true, true
		case b == '\n':
			if m.started {
				return m.endRec(emit, rec)
			}
		case b == '\r':
			m.crBlank = !m.started
			m.st = stCR
		default:
			if err := m.content(b, m.off, emit); err != nil {
				return err
			}
			m.st, m.started = stU, true
		}
	case stU:
		switch {
		case b == ',':
			return m.emitField(emit)
		case b == '"':
			return m.fail(ErrBareQuote)
		case b == '\n':
			return m.endRec(emit, rec)
		case b == '\r':
			m.crBlank = false
			m.st = stCR
		default:
			if err := m.content(b, m.off, emit); err != nil {
				return err
			}
		}
	case stQ:
		switch {
		case b == '"':
			m.st, m.qclose = stQS, m.off+1
		default:
			if err := m.content(b, m.off, emit); err != nil {
				return err
			}
		}
	case stQS:
		switch {
		case b == '"':
			m.st = stQ
			if err := m.content('"', m.off, emit); err != nil {
				return err
			}
		case b == ',':
			if err := m.emitField(emit); err != nil {
				return err
			}
			m.st = stFS
		case b == '\n':
			m.st = stFS
			return m.endRec(emit, rec)
		case b == '\r':
			m.crBlank = false
			m.st = stCR
		default:
			return m.fail(ErrAfterQuote)
		}
	case stCR:
		if b == '\n' {
			m.st = stFS
			if m.crBlank {
				m.off++
				return nil
			}
			return m.endRec(emit, rec)
		}
		m.off-- // 错误位置挂在待定的那个 \r 上
		return m.fail(ErrOrphanCR)
}
	m.off++
	return nil
}

func (m *core) flush(emit func(cell.Cell) error, rec func() error) error {
	switch m.st {
	case stQ, stQS:
		return &Error{Kind: ErrUnclosedQuote, Offset: m.off, Record: m.rec, Field: m.fld}
	case stCR:
		return &Error{Kind: ErrOrphanCR, Offset: m.off - 1, Record: m.rec, Field: m.fld}
	}
	if m.started {
		if err := m.emitField(emit); err != nil {
			return err
		}
		return rec()
	}
	return nil
}

func exitOf(m *core) (Entry, bool) {
	switch m.st {
	case stU:
		return EntryU, false
	case stQ:
		return EntryQ, false
	case stQS:
		return EntryQS, false
	case stCR:
		return EntryFS, true
	default:
		return EntryFS, false
	}
}

// ScanSegment 对 p 按入场状态 entry 完整扫描一遍（段内局部坐标，记录/字段从1）。
// final=true 表示本段就是整个流的末尾（段尾未闭合引号/孤立 CR 按错误报）。
func ScanSegment(p []byte, entry Entry, maxFieldBytes int, final bool) SegResult {
	m := newCore(entry, maxFieldBytes)
	r := SegResult{}
	emit := func(c cell.Cell) error { r.Evs = append(r.Evs, Ev{Field: true, C: c}); return nil }
	rec := func() error { r.Evs = append(r.Evs, Ev{}); return nil }
	for _, b := range p {
		if err := m.step(b, emit, rec); err != nil {
			r.Err = err.(*Error)
			break
		}
	}
	if r.Err == nil && final {
		if err := m.flush(emit, rec); err != nil {
			r.Err = err.(*Error)
		}
	}
	if r.Err == nil && m.started && m.st != stCR {
		c := cell.Cell{Value: m.val.String(), Quoted: m.quoted, Start: m.fstart, End: m.off}
		if m.quoted {
			if m.st == stQS {
				c.End = m.qclose
			}
		}
		r.Pending, r.HasPend = c, true
	}
	if r.Err == nil && m.st == stCR && !m.crBlank {
		c := cell.Cell{Value: m.val.String(), Quoted: false, Start: m.fstart, End: m.off - 1}
		r.Pending, r.HasPend = c, true
	}
	r.Exit, r.CRPending = exitOf(m)
	r.CRBlank = m.crBlank
	return r
}

// Parser 是可多次 Feed 的流式解析前端；非并发安全。
type Parser struct {
	sink      Sink
	m         *core
	processed int64
	term      error
}

// NewParser 创建流式前端；maxFieldBytes<=0 表示不限。
func NewParser(sink Sink, maxFieldBytes int) *Parser {
	return &Parser{sink: sink, m: newCore(EntryFS, maxFieldBytes)}
}

// BytesProcessed 返回状态机处理过的字节总数（每字节恰好一次）。
func (p *Parser) BytesProcessed() int64 { return p.processed }

// Feed 送入一段字节，可调用任意多次。
func (p *Parser) Feed(b []byte) error {
	if p.term != nil {
		return p.term
	}
	p.processed += int64(len(b))
	emit := func(c cell.Cell) error { return p.sink.OnField(c) }
	rec := func() error { return p.sink.OnEndRecord() }
	for _, x := range b {
		if err := p.m.step(x, emit, rec); err != nil {
			p.term = err
			return err
		}
	}
	return nil
}

// Close 结束流；未闭合引号/孤立 CR 在此报错。
func (p *Parser) Close() error {
	if p.term != nil {
		return p.term
	}
	if err := p.m.flush(func(c cell.Cell) error { return p.sink.OnField(c) },
		func() error { return p.sink.OnEndRecord() }); err != nil {
		p.term = err
	}
	return p.term
}
