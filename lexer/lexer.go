// Package lexer 是可暂停续传的 CSV 逐字节状态机，并提供分段运行器供 par 使用。
package lexer

import "ontology/cell"

// Kind 标识彼此可区分的错误类别。
type Kind int

const (
	BareQuote Kind = iota + 1 // 未引号字段中出现 "
	AfterQuote                // 引号闭合后出现非法字符
	UnclosedQuote             // 流结束时引号未闭合
	BareCR                    // 孤立的 \r
	FieldTooLong              // 单字段超上限
	TooManyFields             // 单记录字段数超上限
	TooManyRecords            // 总记录数超上限
	ColumnMismatch            // 记录列数与首条不一致
)

// Error 带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type Error struct {
	Kind   Kind
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	names := [...]string{"", "bare quote", "char after quote", "unclosed quote", "bare CR",
		"field too long", "too many fields", "too many records", "column count mismatch"}
	return "csv: " + names[e.Kind]
}

// Limits 为 0 的项表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Sink 接收状态机产出；返回非 nil 错误会使解析进入终态。
type Sink interface {
	PutCell(c cell.Cell) *Error
	EndRecord(delimOffset int) *Error
}

const (
	stFS = iota // 字段开始
	stBA       // 未引号字段
	stQU       // 引号字段
	stQS       // 引号中刚见 "
	stCR       // 裸 \r 待定
	stQC       // 引号闭合后 \r 待定
)

// Start 是分段运行的入口假设。
const (
StartField  = stFS // 起点在引号外、字段开始
StartQuoted = stQU // 起点在引号字段内（活动单元延续）
)

// End 是分段运行的退出状态。
const (
EndField  = stFS
EndBare   = stBA
EndQuoted = stQU
EndQS     = stQS
EndCR     = stCR
EndQC     = stQC
)

// Ev 是分段事件：Cell 为完成的字段；Rec 为记录边界，Off 为分隔符起始偏移。
type Ev struct {
	C   cell.Cell
	Rec bool
	Off int
}

// SegResult 描述段尾状态；活动单元的信息供协调器跨段拼接。
type SegResult struct {
	End         int
	ActiveLen   int
	ActiveStart int
	ActiveVal   []byte
	Err         *Error
}

type core struct {
	st        int
	val       []byte
	start     int
	quoted    bool
	flen      int
	crOff     int
	hadCell   bool // CR 待定前是否已有活动单元
	maxField  int
	processed int64
}

func (m *core) err(k Kind, off, rec, fld int) *Error {
	return &Error{Kind: k, Offset: off, Record: rec, Field: fld}
}

func (m *core) add(b byte, off, fld int) *Error {
	if m.maxField > 0 && m.flen >= m.maxField {
		return &Error{Kind: FieldTooLong, Offset: off, Field: fld}
	}
	m.val = append(m.val, b)
	m.flen++
	return nil
}

// step 处理一个字节；emit 发出完成字段与记录边界。
func (m *core) step(b byte, off, rec, fld int, emit func(Ev)) *Error {
	m.processed++
	switch m.st {
	case stFS:
		switch {
		case b == ',':
			emit(Ev{C: cell.Cell{Start: off, End: off}, Off: off})
		case b == '"':
			m.st, m.quoted, m.start = stQU, true, off
		case b == '\n':
			// 空行：跳过
		case b == '\r':
			m.st, m.crOff, m.hadCell = stCR, off, false
		default:
			if e := m.add(b, off, fld); e != nil {
				return e
			}
			m.st, m.start = stBA, off
		}
	case stBA:
		switch {
		case b == ',':
			m.finish(off, emit)
		case b == '"':
			return m.err(BareQuote, off, rec, fld)
		case b == '\n':
			m.finish(off, emit)
			emit(Ev{Rec: true, Off: off})
		case b == '\r':
			m.st, m.crOff, m.hadCell = stCR, off, true
		default:
			if e := m.add(b, off, fld); e != nil {
				return e
			}
		}
	case stQU:
		switch b {
		case '"':
			m.st = stQS
		default:
			if e := m.add(b, off, fld); e != nil {
				return e
			}
		}
	case stQS:
		switch {
		case b == ',':
			m.finish(off, emit)
		case b == '"':
			if e := m.add('"', off, fld); e != nil {
				return e
			}
			m.st = stQU
		case b == '\n':
			m.finish(off, emit)
			emit(Ev{Rec: true, Off: off})
		case b == '\r':
			m.st, m.crOff = stQC, off
		default:
			return m.err(AfterQuote, off, rec, fld)
		}
	case stCR:
		switch b {
		case '\n':
			if m.hadCell {
				m.finish(m.crOff, emit)
			}
			emit(Ev{Rec: true, Off: m.crOff})
			m.reset()
		default:
			return m.err(BareCR, m.crOff, rec, fld)
		}
	case stQC:
		if b == '\n' {
			m.finish(m.crOff, emit)
			emit(Ev{Rec: true, Off: m.crOff})
			m.reset()
		} else {
			return m.err(AfterQuote, off, rec, fld)
		}
	}
	return nil
}

func (m *core) finish(delim int, emit func(Ev)) {
	v := m.val
	emit(Ev{C: cell.Cell{Value: v, Quoted: m.quoted, Start: m.start, End: delim}, Off: delim})
	m.reset()
}

func (m *core) reset() {
	m.st, m.val, m.quoted, m.flen, m.hadCell = stFS, nil, false, 0, false
}

// RunSegment 按给定入口假设跑一段，返回事件流与段尾结果。fieldLen 为入口活动单元已有值长度。
func RunSegment(data []byte, start, fieldLen, maxField int) ([]Ev, SegResult) {
	m := core{st: start, quoted: start == stQU, flen: fieldLen, maxField: maxField}
	var evs []Ev
	var e *Error
	for i, b := range data {
		if e = m.step(b, i, 1, 1, func(ev Ev) { evs = append(evs, ev) }); e != nil {
			return evs, SegResult{End: m.st, Err: e}
		}
	}
	r := SegResult{End: m.st, ActiveLen: m.flen, ActiveStart: m.start, ActiveVal: append([]byte(nil), m.val...)}
	return evs, r
}

// Parser 是可多次 Feed 的流式解析器；单实例非并发安全。
type Parser struct {
	sink      Sink
	m         core
	pos       int
	rec, fld  int
	terminal  *Error
	sawRecord bool
}

// NewParser 创建流式解析器。
func NewParser(sink Sink, lim Limits) *Parser {
	return &Parser{sink: sink, m: core{maxField: lim.MaxFieldBytes}, rec: 1, fld: 1}
}

// BytesProcessed 返回状态机处理过的字节总数。
func (p *Parser) BytesProcessed() int64 { return p.m.processed }

func (p *Parser) emit(ev Ev) {
	if ev.Rec {
		p.rec++
		p.fld = 1
		p.sawRecord = true
		if e := p.sink.EndRecord(ev.Off + p.chunkBase); e != nil {
			p.terminal = e
		}
	} else {
		c := ev.C
		c.Start += p.chunkBase
		c.End += p.chunkBase
		if e := p.sink.PutCell(c.Clone()); e != nil {
			p.terminal = e
		}
		p.fld++
	}
}

// Feed 送入一段输入，可任意切分多次调用。
func (p *Parser) Feed(data []byte) error {
	if p.terminal != nil {
		return p.terminal
	}
	base := p.pos
	p.chunkBase = base
	for i, b := range data {
		if e := p.m.step(b, base+i, p.rec, p.fld, p.emitHook()); e != nil {
			e.Record, e.Field, e.Offset = p.rec, p.fld, e.Offset+base
			p.terminal = e
			return e
		}
		if p.terminal != nil {
			return p.terminal
		}
	p.pos += len(data)
	return nil
}

// Close 结束流，刷出最后一条无换行结尾的记录。
func (p *Parser) Close() error {
	if p.terminal != nil {
		return p.terminal
	}
	switch p.m.st {
	case stQU:
		p.terminal = &Error{Kind: UnclosedQuote, Offset: p.m.start + p.chunkBase, Record: p.rec, Field: p.fld}
	case stCR:
		p.terminal = &Error{Kind: BareCR, Offset: p.m.crOff + p.chunkBase, Record: p.rec, Field: p.fld}
	case stQC:
		p.terminal = &Error{Kind: AfterQuote, Offset: p.m.crOff + p.chunkBase, Record: p.rec, Field: p.fld}
	case stBA, stQS:
		c := cell.Cell{Value: append([]byte(nil), p.m.val...), Quoted: p.m.quoted,
			Start: p.m.start + p.chunkBase, End: p.pos}
		if e := p.sink.PutCell(c); e != nil {
			p.terminal = e
			return e
		}
		if e := p.sink.EndRecord(p.pos); e != nil {
			p.terminal = e
		}
	}
	return p.terminal
}
