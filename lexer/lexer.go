package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类可区分的语法错误。
var (
	ErrQuoteInField    = errors.New("lexer: bare '\"' in unquoted field")
	ErrCharsAfterQuote = errors.New("lexer: unexpected character after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unterminated quoted field")
	ErrBareCR          = errors.New("lexer: bare '\\r' not followed by '\\n'")
	ErrClosed          = errors.New("lexer: parser already in terminal error state")
)

// Limits 是可配置的硬上限；0 表示不限制。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
}

// Error 带出错位置：字节偏移（从 0 起）、记录号、字段号（从 1 起）。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

// 状态常量（State 也作为 par 探测快照的入口状态）。
const (
	FS = iota // 字段开始
	U        // 未引号字段中
	Q        // 引号字段中
	QS       // 引号字段中刚见到一个引号
	CR       // 行尾 CR 待定
)

// Event 是状态机产出的原子事件，全部为纯数据。
type Event struct {
	Cell   cell.Cell
	Record bool // true=一条记录在此闭合，Cell 为该记录最后一个字段
	Offset int  // 记录起始偏移
}

// Sink 接收事件。
type Sink interface{ Put(Event) }

// Snapshot 是机器在任意字节边界上的入口状态（供 par 注入）。
type Snapshot struct {
	State      int
	Pos        int
	FieldStart int
	ContentEnd int
	Record     int
	Field      int
	RecordStart int
	Quoted     bool
	Pending    []byte // 切点之前已解码的字段内容前缀
}

// Machine 是逐字节、可暂停续传、可从任意 Snapshot 启动的状态机。
type Machine struct {
	sink   Sink
	limits Limits

	state   int
	pos     int // 下一字节的绝对偏移
	rec     int
	field   int
	recSt   int
	fStart  int
	content []byte
	cEnd    int // 内容已确认覆盖到的字节偏移（半开）
	quoted  bool
	started bool // 当前字段是否已开始
	terminal error

	processed int // 非导出计数器：状态机处理的字节总数
}

// New 创建从空流起点开始的机器。
func New(sink Sink, limits Limits) *Machine {
	m := &Machine{sink: sink, limits: limits, state: FS}
	return m
}

// ResetFromSnapshot 从任意入口快照初始化；pending 是切点前已解码的字段前缀（不计入处理计数）。
func (m *Machine) ResetFromSnapshot(s Snapshot) {
	m.state, m.pos = s.State, s.Pos
	m.rec, m.field, m.recSt = s.Record, s.Field, s.RecordStart
	m.fStart, m.cEnd, m.quoted = s.FieldStart, s.ContentEnd, s.Quoted
	m.content = append(m.content[:0], s.Pending...)
	m.started = s.State != FS || len(m.content) > 0 || s.Quoted
	m.terminal = nil
	m.processed = 0
}

// Processed 返回状态机处理的字节总数。
func (m *Machine) Processed() int { return m.processed }

// Terminal 返回终态错误（nil 表示未进入终态）。
func (m *Machine) Terminal() error { return m.terminal }

func (m *Machine) fail(err error, off int) error {
	m.terminal = &Error{Err: err, Offset: off, Record: m.rec, Field: m.field}
	return m.terminal
}

// Feed 喂入一段字节；一旦出错机器进入终态，之后 Feed 返回同一个错误。
func (m *Machine) Feed(p []byte) error {
	if m.terminal != nil {
		if errors.Is(m.terminal, ErrClosed) {
			return m.terminal
		}
		return m.terminal
	}
	for _, b := range p {
		pos := m.pos
		m.pos++
		m.processed++
		if m.handle(b, pos) {
			return m.terminal
		}
	}
	return nil
}

// 返回 true 表示出错停机。
func (m *Machine) handle(b byte, pos int) bool {
	switch m.state {
	case FS:
		return m.fromFS(b, pos)
	case U:
		return m.fromU(b, pos)
	case Q:
		return m.fromQ(b, pos)
	case QS:
		return m.fromQS(b, pos)
	case CR:
		return m.fromCR(b, pos)
	}
	return false
}
