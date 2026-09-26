// Package lexer 是可暂停续传的 CSV 字节状态机，不依赖 encoding/csv。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类彼此可区分的语法错误。
var (
	ErrBareQuote     = errors.New("bare quote in unquoted field")
	ErrQuoteJunk     = errors.New("unexpected char after closing quote")
	ErrUnterminated  = errors.New("unterminated quoted field")
	ErrLoneCarriage  = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLarge = errors.New("field exceeds max bytes")
)

// Error 携带出错位置：字节偏移、记录号、字段号（均从 1 起，字节偏移从 0 起）。
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

// Sink 接收词法事件。par 回放事件时复用同一个 table sink。
type Sink interface {
	Cell(c cell.Cell, record, field int)
	EndRecord(record int)
}

// Gate 是可选回调：每个成为字段内容的字节到达时先询问是否超限。
// table 在流式路径实现它以做到“第一个超限字节立刻拒绝”。
type Gate interface {
	AllowBytes(n int) error
}

// Frag 是可选回调：报告字段内容增量（par 拼接跨段字段用）。
type Frag interface {
	BeginField(quoted bool, start int)
	Fragment(b byte, offset int)
}

const (
	stStart = iota
	stUnquoted
	stQuoted
	stAfterQuote
	stCR
)

// Snapshot 是某字节偏移处的可恢复状态。BaseRecord/BaseField 是
// 该偏移处“正在进行的记录/字段”的全局 1 基编号；RecsBefore 是
// 已完成记录数；HasPending 表示有一个跨切点开始但未闭合的字段。
type Snapshot struct {
	Offset     int
	State      int
	HasPending bool
	PendQuoted bool
	PendStart  int
	LineOn     bool
}

// Lexer 是增量 CSV 词法分析器。单实例非并发安全。
type Lexer struct {
	sink       Sink
	state      int
	off        int
	recsDone   int
	rec, fld   int
	armed      bool
	quoted     bool
	fieldStart int
	lineOn     bool
	val        []byte
	terminal   error
	processed  int
}

// New 创建 Lexer。
func New(sink Sink) *Lexer { return &Lexer{sink: sink, state: stStart, rec: 1, fld: 1} }

// Processed 返回状态机处理过的字节总数（每字节恰一次）。
func (l *Lexer) Processed() int { return l.processed }

// Terminal 返回终态错误，未终止为 nil。
func (l *Lexer) Terminal() error { return l.terminal }

// Restore 从快照恢复，准备解析其后的字节段。
func Restore(s Snapshot, sink Sink) *Lexer {
	return &Lexer{sink: sink, state: s.State, off: s.Offset, rec: 1, fld: 1,
		armed: s.HasPending, quoted: s.PendQuoted, fieldStart: s.PendStart,
		lineOn: s.LineOn || s.HasPending}
}

// Snapshot 返回处理完当前字节后的可恢复快照（par 在切点处抓取）。
func (l *Lexer) Snapshot() Snapshot {
	return Snapshot{Offset: l.off, State: l.state, HasPending: l.armed,
		PendQuoted: l.quoted, PendStart: l.fieldStart, LineOn: l.lineOn}
}

// EndSnapshot 是 par 在“不执行 EOF 收尾”时需要的段末信息。
type EndSnapshot struct {
	State     int
	HasField  bool
	LineOn    bool
	Quoted    bool
	Start     int
	ValLen    int
	Completed int
	Cells     int
}

// Freeze 导出段末快照（不做 EOF 判定）。
func (l *Lexer) Freeze() EndSnapshot {
	return EndSnapshot{State: l.state, HasField: l.armed, LineOn: l.lineOn,
		Quoted: l.quoted, Start: l.fieldStart, ValLen: len(l.val),
		Completed: l.recsDone, Cells: l.fld - 1}
}

// State 常量导出供 par 的镜像状态机使用。
const (
	StStart      = stStart
	StUnquoted   = stUnquoted
	StQuoted     = stQuoted
	StAfterQuote = stAfterQuote
	StCR         = stCR
)

// Feed 送入一段字节，可调用任意次。
func (l *Lexer) Feed(p []byte) error {
	if l.terminal != nil {
		return l.terminal
	}
	for _, b := range p {
		l.processed++
		if err := l.step(b); err != nil {
			l.terminal = err
			return err
		}
		l.off++
	}
	return nil
}

func (l *Lexer) fail(e error, b int) error {
	return &Error{Err: e, Offset: b, Record: l.rec, Field: l.fld}
}

func (l *Lexer) beginField(quoted bool, pos int) {
	l.armed, l.quoted, l.fieldStart = true, quoted, pos
	l.lineOn = true
	l.val = l.val[:0]
	if f, ok := l.sink.(Frag); ok {
		f.BeginField(quoted, pos)
	}
}

func (l *Lexer) addByte(b byte, pos int) error {
	if g, ok := l.sink.(Gate); ok {
		if err := g.AllowBytes(len(l.val) + 1); err != nil {
			return l.fail(err, pos)
		}
	}
	l.val = append(l.val, b)
	if f, ok := l.sink.(Frag); ok {
		f.Fragment(b, pos)
	}
	return nil
}

func (l *Lexer) emitCell(end int) {
	c := cell.Cell{Value: string(l.val), Quoted: l.quoted, Start: l.fieldStart, End: end}
	l.sink.Cell(c, l.rec, l.fld)
	l.armed = false
	l.fld++
}

func (l *Lexer) endRecord() {
	l.sink.EndRecord(l.rec)
	l.recsDone++
	l.rec, l.fld = l.recsDone+1, 1
}

func (l *Lexer) step(b byte) error {
	pos := l.off
	switch l.state {
	case stStart:
		switch b {
		case ',':
			l.beginField(false, pos)
			l.emitCell(pos)
		case '\n':
			if l.lineOn {
				l.beginField(false, pos)
				l.emitCell(pos)
				l.endRecord()
				l.lineOn = false
			}
		case '\r':
			l.state = stCR
		case '"':
			l.beginField(true, pos)
			l.state = stQuoted
		default:
			l.beginField(false, pos)
			if err := l.addByte(b, pos); err != nil {
				return err
			}
			l.state = stUnquoted
		}
	case stUnquoted:
		switch b {
		case ',':
			l.emitCell(pos)
			l.state = stStart
		case '\n':
			l.emitCell(pos)
			l.endRecord()
			l.state = stStart
		case '\r':
			l.state = stCR
		case '"':
			return l.fail(ErrBareQuote, pos)
		default:
			if err := l.addByte(b, pos); err != nil {
				return err
			}
		}
	case stQuoted:
		switch b {
		case '"':
			l.state = stAfterQuote
		default:
			if err := l.addByte(b, pos); err != nil {
				return err
			}
		}
	case stAfterQuote:
		switch b {
		case '"':
			if err := l.addByte('"', pos); err != nil {
				return err
			}
			l.state = stQuoted
		case ',':
			l.emitCell(pos + 1)
			l.state = stStart
		case '\n':
			l.emitCell(pos + 1)
			l.endRecord()
			l.state = stStart
		case '\r':
			l.state = stCR
		default:
			return l.fail(ErrQuoteJunk, pos+1)
		}
	case stCR:
		if b == '\n' {
			if l.lineOn {
				if !l.armed {
					l.beginField(false, pos-1)
				}
				l.emitCell(pos - 1) // 区间不含 \r\n
				l.endRecord()
			}
			l.lineOn = false
			l.state = stStart
		} else {
			return l.fail(ErrLoneCarriage, pos-1)
		}
	}
	return nil
}

// Close 结束流；返回未终止错误。
func (l *Lexer) Close() error {
	if l.terminal != nil {
		return l.terminal
	}
	switch l.state {
	case stQuoted:
		l.terminal = l.fail(ErrUnterminated, l.off)
	case stCR:
		l.terminal = l.fail(ErrLoneCarriage, l.off-1)
	default:
		if l.armed {
			l.emitCell(l.off)
			l.endRecord()
		} else if l.lineOn {
			l.beginField(false, l.off)
			l.emitCell(l.off)
			l.endRecord()
		}
	}
	return l.terminal
}
