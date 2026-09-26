// Package lexer 是可暂停、可续传的 CSV 字节状态机。每个 Feed 字节只处理一次。
package lexer

import "errors"

// 四类语法错误与孤立 CR 错误，彼此可用 errors.Is 区分。
var (
	ErrBareQuote       = errors.New("lexer: bare '\"' in unquoted field")
	ErrCharsAfterQuote = errors.New("lexer: unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field at EOF")
	ErrLoneCR          = errors.New("lexer: bare '\\r' not followed by '\\n'")
)

// State 是状态机当前状态。
type State int

const (
	SStart State = iota // 字段起点（含行首）
	SUnquoted           // 未引号字段中
	SQuoted             // 引号字段中
	SQuoteEnd           // 引号字段中刚见到一个引号
	SCR                 // \r 待定
)

// Status 是机器的完整快照，供并行切分确定跨段微状态。
type Status struct {
	State       State
	Pending     bool // 下一字段尚未发 Start（逗号/行尾刚发生）
	Started     bool // 当前记录已发过至少一个字段
	CRFromField bool // SCR 是由未引号字段内的 \r 进入（否则是空行位置的 \r）
	CROff       int  // SCR 状态下前导 \r 的偏移
	Pos         int  // 已消费的全局字节数
}

// Sink 接收词法事件。所有偏移为原始输入中的绝对字节偏移。
type Sink interface {
	FieldStart(off int, quoted bool)
	FieldData(p []byte)
	FieldEnd(off int)
	Record()
}

// Machine 是单次 CSV 流的状态机。单实例非并发安全。
type Machine struct {
	sink Sink
	st   State
	pos  int

	pending     bool
	started     bool
	crFromField bool
	crOff       int

	entryQuoted bool
	dead        bool
	deadErr     error
	errOff      int

	processed int
}

// New 构造从字段起点开始的机器。
func New(sink Sink) *Machine { return &Machine{sink: sink, pending: true} }

// NewInQuote 构造"段首已位于引号字段内部"的机器（并行切分用）。
func NewInQuote(sink Sink) *Machine {
	m := New(sink)
	m.st = SQuoted
	m.entryQuoted = true
	return m
}

// Processed 返回状态机处理过的字节总数（非导出语义：白盒测试可读）。
func (m *Machine) Processed() int { return m.processed }

// Status 返回当前快照。
func (m *Machine) Status() Status {
	return Status{m.st, m.pending, m.started, m.crFromField, m.crOff, m.pos}
}

// ErrOffset 返回最近一次语法错误的字节偏移。
func (m *Machine) ErrOffset() int { return m.errOff }

func (m *Machine) startField(off int, quoted bool) {
	m.pending = false
	m.started = true
	m.sink.FieldStart(off, quoted)
}

func (m *Machine) endField(off int) { m.sink.FieldEnd(off) }

func (m *Machine) newline(off int) {
	if m.started {
		m.endField(off)
		m.sink.Record()
	} // started==false：空行，跳过
	m.started = false
	m.pending = true
}

// Feed 喂入一段字节。返回语法错误后机器进入终态。
func (m *Machine) Feed(p []byte) error {
	if m.dead {
		return m.deadErr
	}
	for _, b := range p {
		off := m.pos
		m.pos++
		m.processed++
		if err := m.step(b, off); err != nil {
			m.dead = true
			m.deadErr = err
			m.errOff = off
			if err == ErrLoneCR {
				m.errOff = m.crOff // 指向前导 \r（可能在上一段尾）
			}
			if m.errOff < 0 {
				m.errOff = 0
			}
			return err
		}
	}
	return nil
}

func (m *Machine) step(b byte, off int) error {
	switch m.st {
	case SStart:
		switch b {
		case ',':
			m.startField(off, false)
			m.endField(off)
			m.pending = true
		case '"':
			m.startField(off, true)
			m.st = SQuoted
		case '\n':
			m.newline(off)
		case '\r':
			m.crFromField = false
			m.crOff = off
			m.st = SCR
		default:
			m.startField(off, false)
			m.sink.FieldData([]byte{b})
			m.st = SUnquoted
		}
	case SUnquoted:
		switch b {
		case ',':
			m.endField(off)
			m.pending = true
			m.st = SStart
		case '"':
			return ErrBareQuote
		case '\n':
			m.endField(off)
			m.sink.Record()
			m.pending = true
			m.started = false
			m.st = SStart
		case '\r':
			m.crFromField = true
			m.crOff = off
			m.st = SCR
		default:
			m.sink.FieldData([]byte{b})
		}
	case SQuoted:
		switch b {
		case '"':
			m.st = SQuoteEnd
		default:
			m.sink.FieldData([]byte{b}) // 含 , \n \r：原样保留
		}
	case SQuoteEnd:
		switch b {
		case ',':
			m.endField(off)
			m.pending = true
			m.st = SStart
		case '"':
			m.sink.FieldData([]byte{'"'}) // "" -> 一个引号，计 1 字符
			m.st = SQuoted
		case '\n':
			m.endField(off)
			m.sink.Record()
			m.pending = true
			m.started = false
			m.st = SStart
		case '\r':
			m.crFromField = true
			m.crOff = off
			m.st = SCR
		default:
			return ErrCharsAfterQuote
		}
	case SCR:
		switch b {
		case '\n':
			m.newline(off + 1)
			m.st = SStart
		default:
			return ErrLoneCR // 位置指向前导 \r
		}
	}
	return nil
}

// Close 宣告流结束；处理待定状态与无换行结尾的末记录。
func (m *Machine) Close() error {
	if m.dead {
		return m.deadErr
	}
	switch m.st {
	case SQuoted:
		m.dead, m.deadErr = true, ErrUnclosedQuote
		m.errOff = m.pos
	case SCR:
		m.dead, m.deadErr = true, ErrLoneCR
		m.errOff = m.crOff
	default:
		if m.started {
			m.endField(m.pos)
			m.sink.Record()
			m.started = false
			m.pending = true
		}
	}
	return m.deadErr
}
