// Package lexer 是可暂停续传、可从任意入口状态启动的逐字节 CSV 状态机。
package lexer

import "errors"

// 四类语法错误，彼此可用 errors.Is 区分。
var (
	ErrBareQuote       = errors.New("lexer: bare '\"' in unquoted field")
	ErrQuoteAfterClose = errors.New("lexer: unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field at EOF")
	ErrBareCR          = errors.New("lexer: bare carriage return")
)

// Kind 是事件类别。
type Kind uint8

const (
	KBlank  Kind = iota // 空行（相邻换行），table 层将其丢弃
	KOpen               // 字段开始：Quoted 引号标记，Start 字段首字节偏移
	KValue              // 值片段（引号字段已解码，"" 折叠为一个 "；CRLF 原样）
	KCommit             // 字段结束：原文区间 [Start, End)
	KRecord             // 一条记录结束
	KError              // 致命错误：Err + Start（字节偏移）
)

// Event 是状态机产出的事件。
type Event struct {
	Kind   Kind
	Value  string
	Quoted bool
	Start  int
	End    int
	Err    error
}

// Entry 是分段解析入口状态（par 续接用）。
type Entry uint8

const (
	EBoundary Entry = iota // 干净记录边界/逗号后（FS）
	EUnquoted              // 未引号字段中段
	EQuoted                // 引号字段中段
	EQSeen                 // 刚见引号（QS）
	ECRBare                // \r 待定，前态 FS/UQ
	ECRClose               // \r 待定，前态 QS
)

// Result 是一段字节的解析结果。N 为处理字节数。
type Result struct {
	Events []Event
	Entry  Entry
	Closed bool
	N      int
}

// RunSegment 从 ent 入口解析 data（绝对偏移自 base 起），不追加 EOF。
func RunSegment(data []byte, base int, ent Entry) Result {
	m := &machine{state: stFS, base: base}
	m.init(ent)
	m.run(data)
	return Result{Events: m.ev, Entry: m.entry(), Closed: m.state == stFS && !m.inField, N: m.n}
}

const (
	stFS = iota
	stUQ
	stQ
	stQS
	stCR
)

// machine 同时服务分段与流式解析；n 为非导出的字节处理计数器。
type machine struct {
	state      uint8
	inField    bool
	quoted     bool
	fieldStart int
	cells      int // 当前记录已提交字段数（用于识别空行）
	base       int
	ev         []Event
	fatal      error
	n          int
}

func (m *machine) init(ent Entry) {
	switch ent {
	case EUnquoted:
		m.state, m.inField = stUQ, true
	case EQuoted:
		m.state, m.inField, m.quoted = stQ, true, true
	case EQSeen:
		m.state, m.inField, m.quoted = stQS, true, true
	case ECRBare:
		m.state, m.inField = stCR, true
	case ECRClose:
		m.state, m.inField, m.quoted = stCR, true, true
	}
}

func (m *machine) entry() Entry {
	switch m.state {
	case stQ:
		return EQuoted
	case stQS:
		return EQSeen
	case stCR:
		if m.quoted {
			return ECRClose
		}
		return ECRBare
	case stUQ:
		return EUnquoted
	default:
		return EBoundary
	}
}

func (m *machine) run(data []byte) {
	for i, b := range data {
		m.n++
		m.step(b, m.base+i)
		if m.fatal != nil {
			return
		}
}

func (m *machine) fail(pos int, err error) {
	if m.fatal == nil {
		m.fatal = err
		m.ev = append(m.ev, Event{Kind: KError, Err: err, Start: pos})
	}
}

func (m *machine) open(quoted, pos int) {
	m.inField, m.quoted, m.fieldStart = true, quoted, pos
	m.ev = append(m.ev, Event{Kind: KOpen, Quoted: quoted, Start: pos})
}

func (m *machine) val(b byte) { m.ev = append(m.ev, Event{Kind: KValue, Value: string([]byte{b})}) }

func (m *machine) commit(end int) {
	m.ev = append(m.ev, Event{Kind: KCommit, Quoted: m.quoted, Start: m.fieldStart, End: end})
	m.inField, m.cells = false, m.cells+1
}

func (m *machine) blankOrRecord() {
	k := KRecord
	if m.cells == 0 {
		k = KBlank
	}
	m.cells = 0
	m.ev = append(m.ev, Event{Kind: k})
}

func (m *machine) step(b byte, pos int) {
	switch m.state {
	case stFS:
		m.stepFS(b, pos)
	case stUQ:
		m.stepUQ(b, pos)
	case stQ:
		m.stepQ(b, pos)
	case stQS:
		m.stepQS(b, pos)
	case stCR:
		if b == '\n' {
			m.commit(pos)
			m.blankOrRecord()
			m.state = stFS
			return
		}
		m.fail(pos, ErrBareCR)
	}
}

func (m *machine) stepFS(b byte, pos int) {
	switch b {
	case ',':
		m.open(false, pos)
		m.commit(pos)
	case '"':
		m.open(true, pos)
		m.state = stQ
	case '\r':
		m.inField, m.quoted, m.fieldStart = true, false, pos
		m.state = stCR
	case '\n':
		m.blankOrRecord()
	default:
		m.open(false, pos)
		m.val(b)
		m.state = stUQ
	}
}

func (m *machine) stepUQ(b byte, pos int) {
	switch b {
	case ',':
		m.commit(pos)
		m.open(false, pos+1)
		m.commit(pos+1)
	case '"':
		m.fail(pos, ErrBareQuote)
	case '\r':
		m.state = stCR
	case '\n':
		m.commit(pos)
		m.blankOrRecord()
		m.state = stFS
	default:
		m.val(b)
	}
}

func (m *machine) stepQ(b byte, pos int) {
	if b == '"' {
		m.state = stQS
		return
	}
	m.val(b)
}

func (m *machine) stepQS(b byte, pos int) {
	switch b {
	case '"':
		m.val('"')
		m.state = stQ
	case ',':
		m.commit(pos)
		m.open(false, pos+1)
		m.commit(pos+1)
	case '\r':
		m.quoted = true
		m.state = stCR
	case '\n':
		m.commit(pos)
		m.blankOrRecord()
		m.state = stFS
	default:
		m.fail(pos, ErrQuoteAfterClose)
	}
}

// finish 施加 EOF 语义。totalLen 为流总字节数（用于末字段偏移）。
func (m *machine) finish(totalLen int) error {
	if m.fatal != nil {
		return m.fatal
	}
	switch m.state {
	case stQ:
		m.fail(totalLen, ErrUnclosedQuote)
	case stCR:
		m.fail(totalLen, ErrBareCR)
	case stUQ, stQS:
		m.commit(totalLen)
		m.cells = 0
		m.ev = append(m.ev, Event{Kind: KRecord})
	case stFS:
		if m.inField {
			m.commit(totalLen)
			m.ev = append(m.ev, Event{Kind: KRecord})
		}
	}
	return m.fatal
}

// EOFError 仅在分段测试中需要时暴露：检查某 Result 续到 EOF 的终态错误。
func EOFError(r Result, totalLen int) error {
	m := &machine{state: stateOf(r.Entry), ev: r.Events}
	m.init(r.Entry)
	m.state = stateOf(r.Entry)
	return m.finish(totalLen)
}

func stateOf(e Entry) uint8 {
	switch e {
	case EQuoted:
		return stQ
	case EQSeen:
		return stQS
	case ECRBare, ECRClose:
		return stCR
	case EUnquoted:
		return stUQ
	default:
		return stFS
	}
}
