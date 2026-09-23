// Package lexer 是可暂停/续传的逐字节 CSV 状态机，也是 par 的段执行器。
// 单个 Machine 不要求并发安全；par 为每段使用独立 Machine。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类彼此可区分的语法错误与三类上限错误，用 errors.Is 判定。
var (
	ErrBareQuote      = errors.New("lexer: bare '\"' in unquoted field")
	ErrQuoteTrailing  = errors.New("lexer: character after closing quote")
	ErrQuoteNotClosed = errors.New("lexer: unclosed quoted field")
	ErrBareCR         = errors.New("lexer: bare '\\r' not followed by '\\n'")
	ErrFieldTooLong   = errors.New("lexer: field exceeds max bytes")
	ErrTooManyFields  = errors.New("lexer: too many fields in record")
	ErrTooManyRecords = errors.New("lexer: too many records")
)

// ErrPos 携带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type ErrPos struct {
	Err            error
	Offset         int64
	Record, Field  int
}

func (e *ErrPos) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *ErrPos) Unwrap() error { return e.Err }

// Limits 为可配置上限，0 表示不限。
type Limits struct {
	MaxFieldBytes int64
	MaxFields     int
	MaxRecords    int
}

// Event：Kind 'f' 发字段、'r' 发记录结束。
type Event struct {
	Kind byte
	Cell cell.Cell
}

// Sink 接收状态机事件。
type Sink interface{ Emit(Event) error }

// State 是段结束时机内状态。
type State int

const (
	Start State = iota // 引号外、记录起点、尚无字段
	Unq                // 引号外，字段已开始
	InQ                // 引号字段中
	AfterQ             // 引号字段中刚见一个引号
	AfterQCR           // 闭合引号后见 \r
	CRP                // 未引号态见 \r 待定
)

// Machine 是无并发安全保证的流式状态机。
type Machine struct {
	sink Sink
	lim  Limits

	st                             State
	off                            int64
	nrec, nfield                   int
	val                            []byte
	quoted, started, atRecStart    bool
	fstart                         int64
	bytes                          int64
	frozen                         error
	mode                           int // 0 流式；1 段-Start 假设；2 段-InQ 假设
	leadBare, firstFieldNotDone    bool
}

// New 创建流式状态机。
func New(sink Sink, lim Limits) *Machine {
	return &Machine{sink: sink, lim: lim, st: Start, atRecStart: true}
}

// BytesProcessed 返回被状态机处理的字节总次数（流式下恰为输入字节数）。
func (m *Machine) BytesProcessed() int64 { return m.bytes }

// Records/Fields 返回已完成记录数与当前记录已发字段数。
func (m *Machine) Records() int { return m.nrec }
func (m *Machine) Fields() int  { return m.nfield }

func (m *Machine) errAt(e error) *ErrPos {
	f := m.nfield + 1
	if f == 1 && m.nfield == 0 {
	}
	return &ErrPos{Err: e, Offset: m.off, Record: m.nrec + 1, Field: f}
}

func (m *Machine) grow(b byte) *ErrPos {
	m.val = append(m.val, b)
	if m.lim.MaxFieldBytes > 0 && int64(len(m.val)) > m.lim.MaxFieldBytes {
		return m.errAt(ErrFieldTooLong)
	}
	return nil
}

func (m *Machine) begin() {
	if !m.started {
		m.fstart, m.started = m.off, true
	}
}

func (m *Machine) emitField() *ErrPos {
	m.nfield++
	c := cell.Cell{Value: string(m.val), Quoted: m.quoted, Start: m.fstart, End: m.off}
	m.val, m.quoted, m.started = m.val[:0], false, false
	m.atRecStart = false
	if err := m.sink.Emit(Event{Kind: 'f', Cell: c}); err != nil {
		return asErr(err)
	}
	return nil
}

func (m *Machine) emitRec() *ErrPos {
	m.nrec, m.nfield, m.atRecStart = m.nrec+1, 0, true
	if err := m.sink.Emit(Event{Kind: 'r'}); err != nil {
		return asErr(err)
	}
	return nil
}

func asErr(err error) *ErrPos {
	if ep, ok := err.(*ErrPos); ok {
		return ep
	}
	return &ErrPos{Err: err}
}

func (m *Machine) fieldOK() *ErrPos {
	if m.lim.MaxFields > 0 && m.nfield+1 > m.lim.MaxFields {
		return m.errAt(ErrTooManyFields)
	}
	return m.emitField()
}

func (m *Machine) recOK() *ErrPos {
	if m.lim.MaxRecords > 0 && m.nrec+1 > m.lim.MaxRecords {
		return m.errAt(ErrTooManyRecords)
	}
	return m.emitRec()
}
