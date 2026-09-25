// Package lexer 是可暂停续传的 CSV 逐字节状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类语法错误 + 三类上限错误，彼此可判定。
var (
	ErrQuoteInPlain   = errors.New("bare '\"' in unquoted field")
	ErrCharsAfterQuote = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote  = errors.New("unterminated quoted field")
	ErrLoneCR         = errors.New("lone carriage return")
	ErrFieldTooLarge  = errors.New("field exceeds byte limit")
	ErrTooManyFields  = errors.New("record exceeds field count limit")
	ErrTooManyRecords = errors.New("record count limit exceeded")
	ErrArity          = errors.New("record field count differs from first record")
)

// Limits 为 0 的项表示不限。
type Limits struct {
	MaxFieldBytes    int
	MaxFieldsRecord  int
	MaxRecords       int
}

// PosError 携带字节偏移（从 0 起）、记录号、字段号（从 1 起）。
type PosError struct {
	Err           error
	Offset        int
	Record, Field int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%s at byte %d (record %d field %d)", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// Sink 接收词法事件：字段关闭、记录关闭（Off 为该事件定界字节的绝对偏移）。
type Sink interface {
	Field(c cell.Cell) error
	Record(off int) error
}

// 尾状态类型。
const (
	StStart = iota // 字段起点（可能刚过逗号，空字段待开始）
	StPlain        // 未引号字段进行中
	StQuoted       // 引号字段进行中
	StQSeen        // 引号字段刚见到闭合引号，等定界
	StCR1          // 未引号字段后的 \r 待定（字段未发）
	StCR2          // 字段已发后的 \r 待定
)

// Tail 是一段输入结束时的快照（供 par 拼接）。
type Tail struct {
	State          int
	Value          string // 进行中字段的已累积值
	Start          int    // 进行中字段起点（inside 段为段首标记）
	Quoted         bool
	Bytes          int // 进行中字段的值字节计数
	CRPos          int
	Recs, Fields   int // 本段已关记录数；尾部未关记录中已发字段数
}

type state int

const (
	sStart state = iota
	sPlain
	sQuoted
	sQSeen
	sCR1
	sCR2
)

// Machine 是单实例状态机；单实例非并发安全。
type Machine struct {
	sink                           Sink
	lim                            Limits
	st                             state
	pos                            int
	val                            []byte
	start                          int
	quoted, started                bool
	bytes                          int
	crpos                          int
	recNo, fieldNo                 int
	recs, fields                   int
	processed                      bool
	cur                            bool // 当前记录是否已见到字节
	count                          int
	err                            *PosError
}

// New 创建从字段起点开始的状态机。
func New(sink Sink, base int, lim Limits) *Machine {
	m := &Machine{sink: sink, lim: lim, pos: base, start: base}
	return m
}

// NewContinued 供 par 跨段回退重算：起点在一个已开始字段中。
func NewContinued(sink Sink, base int, insideQuoted bool, startBytes int, lim Limits) *Machine {
	m := New(sink, base, lim)
	m.bytes = startBytes
	if insideQuoted {
		m.st, m.quoted, m.started, m.start = sQuoted, true, true, base
	} else {
		m.st, m.started, m.start = sPlain, true, base
	}
	return m
}

// Count 返回状态机处理过的字节总数（每字节恰好一次）。
func (m *Machine) Count() int { return m.count }

// Err 返回终态错误。
func (m *Machine) Err() *PosError { return m.err }

func (m *Machine) fail(e error, off, rec, field int) bool {
	m.err = &PosError{Err: e, Offset: off, Record: rec, Field: field}
	return false
}

func (m *Machine) emit(off int) bool {
	c := cell.Cell{Value: string(m.val), Quoted: m.quoted, Start: m.start, End: off}
	if err := m.sink.Field(c); err != nil {
		m.err = asPos(err, off, m.recs+1, m.fieldNo+1)
		return false
	}
	m.fields++
	m.fieldNo++
	m.val, m.quoted, m.started, m.bytes = nil, false, false, 0
	return true
}

func (m *Machine) record(off int) bool {
	if err := m.sink.Record(off); err != nil {
		m.err = asPos(err, off, m.recs+1, m.fieldNo+1)
		return false
	}
	m.recs++
	m.recNo++
	m.fields, m.fieldNo = 0, 0
	m.cur = false
	m.start = m.pos + 1
	return true
}

func asPos(err error, off, rec, field int) *PosError {
	var pe *PosError
	if errors.As(err, &pe) {
		return pe
	}
	return &PosError{Err: err, Offset: off, Record: rec, Field: field}
}

func (m *Machine) add(b byte, off int) bool {
	m.val = append(m.val, b)
	m.bytes++
	if m.lim.MaxFieldBytes > 0 && m.bytes > m.lim.MaxFieldBytes {
		return m.fail(ErrFieldTooLarge, off, m.recs+1, m.fieldNo+1)
	}
	return true
}

// Feed 推入一段字节；可任意多次调用。
func (m *Machine) Feed(p []byte) *PosError {
	for _, b := range p {
		if m.err != nil {
			return m.err
		}
		m.count++
		m.processed = true
		m.cur = true
		off := m.pos
		m.pos++
		if !m.step(b, off) {
			return m.err
		}
	}
	return nil
}

func (m *Machine) step(b byte, off int) bool {
	switch m.st {
	case sStart:
		switch b {
		case ',':
			if !m.emit(off) {
				return false
			}
			m.start = m.pos
		case '"':
			m.st, m.quoted, m.started, m.start = sQuoted, true, true, off
		case '\r':
			m.st, m.crpos = sCR1, off
		case '\n':
			if !m.emit(off) || !m.record(off) {
				return false
			}
			m.start = m.pos
		default:
			m.st, m.started, m.start = sPlain, true, off
			if !m.add(b, off) {
				return false
			}
		}
	case sPlain:
		switch b {
		case ',':
			if !m.emit(off) {
				return false
			}
			m.st, m.start = sStart, m.pos
		case '\r':
			m.st, m.crpos = sCR1, off
		case '\n':
			if !m.emit(off) || !m.record(off) {
				return false
			}
			m.st, m.start = sStart, m.pos
		case '"':
			return m.fail(ErrQuoteInPlain, off, m.recs+1, m.fieldNo+1)
		default:
			if !m.add(b, off) {
				return false
			}
		}
	case sQuoted:
		if b == '"' {
			m.st = sQSeen
		} else if !m.add(b, off) {
			return false
		}
	case sQSeen:
		switch b {
		case '"':
			m.st = sQuoted
			if !m.add('"', off) {
				return false
			}
		case ',':
			if !m.emit(off) {
				return false
			}
			m.st, m.start = sStart, m.pos
		case '\n':
			if !m.emit(off) || !m.record(off) {
				return false
			}
			m.st, m.start = sStart, m.pos
		case '\r':
			if !m.emit(off) {
				return false
			}
			m.st, m.crpos = sCR2, off
		default:
			return m.fail(ErrCharsAfterQuote, off, m.recs+1, m.fieldNo+1)
		}
	case sCR1:
		if b != '\n' {
			return m.fail(ErrLoneCR, m.crpos, m.recs+1, m.fieldNo+1)
		}
		if !m.emit(m.crpos) || !m.record(off) {
			return false
		}
		m.st, m.start = sStart, m.pos
	case sCR2:
		if b != '\n' {
			return m.fail(ErrLoneCR, m.crpos, m.recs+1, m.fieldNo)
		}
		if !m.record(off) {
			return false
		}
		m.st, m.start = sStart, m.pos
	}
	return true
}

// Tail 返回当前尾快照（不做 EOF 判定）。
func (m *Machine) Tail() Tail {
	t := Tail{State: int(m.st), Value: string(m.val), Start: m.start, Quoted: m.quoted,
		Bytes: m.bytes, CRPos: m.crpos, Recs: m.recs, Fields: m.fields}
	return t
}

// EOF 宣告流结束，处理隐式记录并返回可能的终态错误。
func (m *Machine) EOF() *PosError {
	if m.err != nil {
		return m.err
	}
	switch m.st {
	case sCR1:
		m.fail(ErrLoneCR, m.crpos, m.recs+1, m.fieldNo+1)
	case sCR2:
		m.fail(ErrLoneCR, m.crpos, m.recs+1, m.fieldNo)
	case sQuoted:
		m.fail(ErrUnclosedQuote, m.pos, m.recs+1, m.fieldNo+1)
	default:
		if m.cur {
			end := m.pos
			if !m.emit(end) {
				return m.err
			}
			m.record(end)
		}
	}
	return m.err
}

// Processed 报告本段是否被喂入过字节。
func (m *Machine) Processed() bool { return m.processed }

// Collector 是把事件收集到内存的 Sink（par worker 使用）。
type Collector struct {
	Events []Event
}

// Event 为一个词法事件。Field=false 时为记录事件。
type Event struct {
	Field bool
	C     cell.Cell
	Off   int
}

func (c *Collector) Field(cell cell.Cell) error {
	c.Events = append(c.Events, Event{Field: true, C: cell})
	return nil
}

func (c *Collector) Record(off int) error {
	c.Events = append(c.Events, Event{Off: off})
	return nil
}
