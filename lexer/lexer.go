// Package lexer 是可暂停续传的逐字节 CSV 状态机，只产出词法事件。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 四类语法错误彼此独立，外加孤立 CR。
var (
	ErrQuoteInBare    = errors.New("csv: bare quote in unquoted field")
	ErrCharsAfterQuote = errors.New("csv: unexpected chars after closing quote")
	ErrUnclosedQuote  = errors.New("csv: unclosed quoted field")
	ErrBareCR         = errors.New("csv: bare carriage return")
	ErrFieldTooLong   = errors.New("csv: field too long")
)

// State 是状态机当前状态。
type State int

const (
	FStart State = iota // 字段开始
	Bare                // 未引号字段中
	Quoted              // 引号字段中
	QQuote              // 引号字段中刚见到一个引号
	CR                  // 行尾 CR 待定
)

// Kind 标识词法事件种类。
type Kind int

const (
	KCell Kind = iota // 一个字段闭合
	KEOL              // 一个记录在 \n（或 \r\n）处闭合
	KErr              // 语法错误
)

// Event 是状态机产出的事件。KCell 时 Cell 有效；KErr 时 Err/Off 有效。
// CR=true 表示该 EOL 由 \r\n 产生。
type Event struct {
	Kind Kind
	Cell cell.Cell
	CR   bool
	Err  error
	Off  int
}

// Sink 接收事件，返回非 nil 即中止处理。
type Sink func(Event) error

// Machine 是纯状态机：非并发安全，可跨多次 Run 续传。
type Machine struct {
	st        State
	val       []byte
	start     int  // 当前字段原文起点；-1 表示行首尚未开始
	opened    bool // 当前字段是否引号字段
	pending   bool // 有引号已闭合、等待分隔符的 cell 挂起
	cells     int  // 当前记录已闭合 cell 数
	n         int  // 当前字段内容字节计数（含跨段前缀）
	maxField  int  // 单字段上限；0 不限
	processed int
}

// New 创建处于 FStart 的状态机。
func New() *Machine { return &Machine{start: -1} }

// NewInside 创建"起点位于引号字段内部"的假设机：
// 携带跨段前缀的字段起点与已计内容长度。
func NewInside(maxField, startPos, prefixLen int) *Machine {
	return &Machine{st: Quoted, opened: true, start: startPos, n: prefixLen, maxField: maxField}
}

// WithFieldLimit 给状态机设置单字段字节上限。
func (m *Machine) WithFieldLimit(n int) *Machine { m.maxField = n; return m }

// State 返回当前状态。
func (m *Machine) State() State { return m.st }

// Processed 返回字节被状态机处理的总次数。
func (m *Machine) Processed() int { return m.processed }

// Snap 是一段结束时的可序列化续传状态。
// OpenLen 是跨段未闭合 cell 已累计的内容字节数；OpenStart 是其原文起点。
type Snap struct {
	St        State
	Val       string
	OpenLen   int
	OpenStart int
	Pending   bool
}

// Snapshot 取当前状态快照。
func (m *Machine) Snapshot() Snap {
	return Snap{St: m.st, Val: string(m.val), OpenLen: m.n, OpenStart: m.start, Pending: m.pending}
}

// Resume 从快照恢复一个续传机（用于 QQuote 边界的精确续接）。
func Resume(s Snap) *Machine {
	return &Machine{st: s.St, val: []byte(s.Val), start: s.OpenStart,
		opened: s.St == Quoted, n: s.OpenLen, pending: s.Pending}
}

func (m *Machine) cell(c cell.Cell, sink Sink) error {
	m.cells++
	m.n = 0
	return sink(Event{Kind: KCell, Cell: c})
}

func (m *Machine) add(b byte, off int, sink Sink) error {
	m.val = append(m.val, b)
	m.n++
	if m.maxField > 0 && m.n > m.maxField {
		return sink(Event{Kind: KErr, Err: ErrFieldTooLong, Off: off})
	}
	return nil
}

func (m *Machine) eol(off int, sink Sink) error {
	m.cells = 0
	m.start = -1
	m.pending = false
	return sink(Event{Kind: KEOL, Off: off})
}

// Run 处理 p；base 是 p[0] 的全局偏移。
func (m *Machine) Run(p []byte, base int, sink Sink) error {
	for i, b := range p {
		m.processed++
		off := base + i
		switch m.st {
		case FStart:
			switch b {
			case ',':
				if m.start < 0 {
					m.start = off
				}
				if err := m.cell(cell.Cell{Start: m.start, End: off}, sink); err != nil {
					return err
				}
				m.start = off + 1
			case '"':
				if m.start < 0 {
					m.start = off
				}
				m.opened = true
				m.st = Quoted
			case '\r':
				if m.start < 0 {
					m.start = off
				}
				m.st = CR
			case '\n':
				if m.start < 0 {
					m.start = off
				}
				if err := m.cell(cell.Cell{Start: m.start, End: off}, sink); err != nil {
					return err
				}
				if err := m.eol(off, sink); err != nil {
					return err
				}
			default:
				if m.start < 0 {
					m.start = off
				}
				if err := m.add(b, off, sink); err != nil {
					return err
				}
				m.st = Bare
			}
		case Bare:
			switch b {
			case ',':
				if err := m.cell(cell.Cell{Value: string(m.val), Start: m.start, End: off}, sink); err != nil {
					return err
				}
				m.val = m.val[:0]
				m.start = off + 1
				m.st = FStart
			case '"':
				return sink(Event{Kind: KErr, Err: ErrQuoteInBare, Off: off})
			case '\r':
				m.st = CR
			case '\n':
				if err := m.cell(cell.Cell{Value: string(m.val), Start: m.start, End: off}, sink); err != nil {
					return err
				}
				m.val = m.val[:0]
				if err := m.eol(off, sink); err != nil {
					return err
				}
				m.st = FStart
			default:
				if err := m.add(b, off, sink); err != nil {
					return err
				}
			}
		case Quoted:
			if b == '"' {
				m.st = QQuote
			} else {
				if err := m.add(b, off, sink); err != nil {
					return err
				}
			}
		case QQuote:
			switch b {
			case '"':
				if err := m.add('"', off, sink); err != nil {
					return err
				}
				m.st = Quoted
			case ',':
				if err := m.cell(cell.Cell{Value: string(m.val), Quoted: true, Start: m.start, End: off}, sink); err != nil {
					return err
				}
				m.val = m.val[:0]
				m.start = off + 1
				m.pending = false
				m.st = FStart
			case '\r':
				m.pending = true
				m.st = CR
			case '\n':
				if err := m.cell(cell.Cell{Value: string(m.val), Quoted: true, Start: m.start, End: off}, sink); err != nil {
					return err
				}
				m.val = m.val[:0]
				if err := m.eol(off, sink); err != nil {
					return err
				}
				m.st = FStart
			default:
				return sink(Event{Kind: KErr, Err: ErrCharsAfterQuote, Off: off})
			}
		case CR:
			if b != '\n' {
				return sink(Event{Kind: KErr, Err: ErrBareCR, Off: off - 1})
			}
			if m.pending {
				if err := m.cell(cell.Cell{Value: string(m.val), Quoted: true, Start: m.start, End: off - 1}, sink); err != nil {
					return err
				}
				m.val = m.val[:0]
			} else {
				if err := m.cell(cell.Cell{Value: string(m.val), Quoted: m.opened, Start: m.start, End: off - 1}, sink); err != nil {
					return err
				}
				m.val = m.val[:0]
				m.opened = false
			}
			m.pending = false
			if err := sink(Event{Kind: KEOL, CR: true}); err != nil {
				return err
			}
			m.cells = 0
			m.start = -1
			m.st = FStart
		}
	}
	return nil
}

// Finish 在流末处理挂起状态。
func (m *Machine) Finish(base int, sink Sink) error {
	switch m.st {
	case Quoted:
		return sink(Event{Kind: KErr, Err: ErrUnclosedQuote, Off: m.start})
	case CR:
		return sink(Event{Kind: KErr, Err: ErrBareCR, Off: base - 1})
	case QQuote:
		return m.cell(cell.Cell{Value: string(m.val), Quoted: true, Start: m.start, End: base}, sink)
	case Bare:
		return m.cell(cell.Cell{Value: string(m.val), Start: m.start, End: base}, sink)
	}
	return nil
}
