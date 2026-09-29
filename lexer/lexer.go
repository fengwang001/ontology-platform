package lexer

import (
	"errors"

	"ontology/cell"
)

// 可判定哨兵：四类语法错误、孤立 CR、三类上限、终态写入。
var (
	ErrQuoteInBare  = errors.New("unescaped quote in unquoted field")
	ErrTextAfterQ   = errors.New("unexpected character after closing quote")
	ErrUnclosedQ    = errors.New("unclosed quoted field")
	ErrFieldCount   = errors.New("wrong number of fields in record")
	ErrLoneCR       = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLong = errors.New("field exceeds max bytes")
	ErrTooManyField = errors.New("record exceeds max fields")
	ErrTooManyRec   = errors.New("too many records")
	ErrTerminal     = errors.New("parser already in terminal state")
)

// 状态常量（供 par 假设注入使用）。
const (
	StateFieldStart = iota
	StateBare
	StateQuoted
	StateAfterQuote
	StateCR
)

// Error 携带字节偏移（0 起）、记录号、字段号（1 起）。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Limits 配置三类上限，0 表示不限。
type Limits struct {
	MaxFieldBytes   int
	MaxRecordFields int
	MaxRecords      int
}

// Sink：Field 一个完整字段（Value 已解码）；OpenField 跨切点未闭合字段的原始片段；
// RecordEnd 记录结束偏移（终止符后一字节）；Blank 空行跳过；Error 终结性错误。
type Sink interface {
	Field(c cell.Cell)
	OpenField(off, end int, quoted bool)
	RecordEnd(off int)
	Blank()
Error(e *Error)
}

// Resume 是段扫描初始上下文。Base 为该段全局起始偏移。
type Resume struct {
	State       int
	Base        int
	BaseRecords int
	BaseFields  int
	OpenOff     int
	OpenQuoted  bool
	PendingCR   bool
	OptU        bool // OUT 乐观模式：仅首字节的 " 视为开引号
	Limits      Limits
}

// Result 是 Run 的结束上下文，供下一段拼接。
type Result struct {
	State      int
	Recs       int
	OpenOff    int
	OpenQuoted bool
	Open       bool // 段末是否有未闭合字段
	PendingCR  bool
}

type machine struct {
	r         Resume
	s         Sink
	state     int
	off       int
	nrec      int
	fcnt      int
	fstart    int
	flen      int
	quoted    bool
	pendingCR bool
	pending   bool // 当前记录是否已见内容
	fieldOpen bool
	dead      bool
}

func (m *machine) fail(err error, at int) {
	if m.dead {
		return
	}
	m.dead = true
	m.s.Error(&Error{Err: err, Offset: at, Record: m.r.BaseRecords + m.nrec + 1,
		Field: m.fcnt + 1})
}

func (m *machine) grow(at, n int) bool {
	if m.r.Limits.MaxFieldBytes > 0 && m.flen+n > m.r.Limits.MaxFieldBytes {
		m.fail(ErrFieldTooLong, at)
		return false
	}
	m.flen += n
	return true
}

// Run 对 buf 做一次纯函数状态机扫描（字节绝对坐标由 Resume.Base 注入）。
func Run(buf []byte, r Resume, s Sink) Result {
	m := &machine{r: r, s: s, state: r.State, off: r.Base, nrec: 0,
		fcnt: r.BaseFields, fstart: r.OpenOff, quoted: r.OpenQuoted, pendingCR: r.PendingCR}
	if r.PendingCR {
		m.state = StateCR
	}
	for i := 0; i < len(buf) && !m.dead; i++ {
		m.off = r.Base + i
		m.step(buf[i])
	}
	return Result{State: m.state, Recs: m.nrec, OpenOff: m.fstart,
		OpenQuoted: m.quoted, Open: m.fieldOpen && !m.dead, PendingCR: m.state == StateCR}
}

// EOF 在流末消解待定状态；返回是否发生错误。
func EOF(r Resume, recs int, s Sink) *Result {
	m := &machine{r: r, s: s, state: r.State, off: r.Base, nrec: recs,
		fcnt: r.BaseFields, fstart: r.OpenOff, quoted: r.OpenQuoted, pendingCR: r.PendingCR}
	if r.PendingCR {
		m.state = StateCR
	}
	m.atEOF()
	return &Result{State: m.state, Recs: m.nrec, OpenOff: m.fstart,
		OpenQuoted: m.quoted, Open: m.fieldOpen && !m.dead, PendingCR: false}
}

func (m *machine) beginField(at int, quoted bool) {
	if m.r.Limits.MaxRecordFields > 0 && m.fcnt-m.r.BaseFields >= m.r.Limits.MaxRecordFields {
		m.fail(ErrTooManyField, at)
		return
	}
	m.fcnt++
	m.fstart, m.flen, m.quoted, m.fieldOpen = at, 0, quoted, true
}

func (m *machine) emitField(at int) {
	m.s.Field(cell.New("", m.quoted, m.fstart, at))
	m.fieldOpen = false
}

func (m *machine) endRecord(at int) {
	if !m.pending {
		m.s.Blank()
	} else {
		m.s.RecordEnd(at)
		m.nrec++
		if m.r.Limits.MaxRecords > 0 && m.r.BaseRecords+m.nrec > m.r.Limits.MaxRecords {
			m.fail(ErrTooManyRec, at)
		}
	}
	m.pending, m.fieldOpen = false, false
}

// 占位：step/atEOF 在下一轮补全。
func (m *machine) step(b byte)  {}
func (m *machine) atEOF()     {}
