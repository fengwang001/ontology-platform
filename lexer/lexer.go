// Package lexer 是逐字节、可暂停可续传的 CSV 状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 状态。
const (
	S0  = 0 // 字段起始
	SU  = 1 // 未引号字段中
	SQ  = 2 // 引号字段中
	SQP = 3 // 引号字段中刚见到引号
	CRU = 4 // 未引号字段中 \r 待定
	CRQ = 5 // 引号字段中 \r 待定
)

var (
	ErrBareQuote     = errors.New("bare '\"' in unquoted field")
	ErrQuoteClosed   = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote = errors.New("unclosed quoted field")
	ErrBareCR        = errors.New("bare '\\r' not followed by '\\n'")
	ErrFieldTooLong  = errors.New("field exceeds MaxFieldBytes")
	ErrTerminal      = errors.New("parser already in terminal state")
)

// PosError 携带字节偏移（从 0 起）并包装哨兵错误。
type PosError struct {
	Err error
	Off int
}

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

// Limits 是可配置上限；0 表示不限。
type Limits struct{ MaxFieldBytes int }

// Sink 接收词法事件。返回非 nil 错误会使状态机进入终态。
type Sink interface {
	Field(c cell.Cell) error
	Line(blank bool, off int) error
}

// Ev 是段解析事件：Field 为真时是字段，否则是行尾。
type Ev struct {
	IsField bool
	Blank   bool
	C       cell.Cell
	Off     int
}

// State 是可序列化的运行点（供 par 拼接）。
type State struct {
	Mode   int
	Val    string
	Quoted bool
	Start  int
	Flen   int
	Began  bool
}

// SegResult 是一段的解析结果。
type SegResult struct {
	Evs []Ev
	End State
	Err *PosError
}

// Lexer 是流式状态机。单实例非并发安全。
type Lexer struct {
	sink   Sink
	lim    Limits
	off    int
	mode   int
	val    []byte
	quoted bool
	start  int
	flen   int
	began  bool
	dead   error
	bytes  int64 // 非导出：被处理字节总数
	evs    []Ev
}

// New 创建流式状态机。
func New(sink Sink, lim Limits) *Lexer {
	return &Lexer{sink: sink, lim: lim, start: -1}
}

// BytesProcessed 返回状态机处理过的字节数。
func (l *Lexer) BytesProcessed() int64 { return l.bytes }

// Terminal 返回终态错误；nil 表示尚未终止。
func (l *Lexer) Terminal() error { return l.dead }

// Feed 喂入一段字节，可调用任意多次。
func (l *Lexer) Feed(p []byte) error {
	if l.dead != nil {
		return ErrTerminal
	}
	return l.run(p)
}

// Close 结束流：flush 无尾换行的末记录并检查未闭合引号。
func (l *Lexer) Close() error {
	if l.dead != nil {
		return l.dead
	}
	switch l.mode {
	case SQ, SQP:
		l.fail(ErrUnclosedQuote, l.start)
	case CRU:
		l.fail(ErrBareCR, l.off-1)
	default:
		if l.began || l.mode != S0 {
			l.emitField(l.start, l.off)
			if err := l.emitLine(false, l.off); err != nil {
				return err
			}
		}
	}
	return l.dead
}

// RunSegment 从入口状态 init 解析 p（base 为 p 首字节全局偏移），不做 EOF 检查。
func RunSegment(p []byte, base int, init State, lim Limits) SegResult {
	l := &Lexer{lim: lim, off: base, mode: init.Mode, val: []byte(init.Val),
		quoted: init.Quoted, start: init.Start, flen: init.Flen, began: init.Began}
	l.run(p)
	end := State{l.mode, string(l.val), l.quoted, l.start, l.flen, l.began}
	pe, _ := l.dead.(*PosError)
	return SegResult{l.evs, end, pe}
}

func (l *Lexer) fail(k error, off int) {
	if l.dead == nil {
		l.dead = &PosError{Err: k, Off: off}
	}
}

func (l *Lexer) put(b byte) bool {
	if l.lim.MaxFieldBytes > 0 && l.flen >= l.lim.MaxFieldBytes {
		l.fail(ErrFieldTooLong, l.off-1)
		return false
	}
	l.flen++
	l.val = append(l.val, b)
	return true
}

func (l *Lexer) emitField(start, end int) error {
	c := cell.New(string(l.val), l.quoted, start, end)
	l.val, l.quoted, l.start, l.flen = nil, false, -1, 0
	l.began = true
	if l.sink != nil {
		return l.sink.Field(c)
	}
	l.evs = append(l.evs, Ev{IsField: true, C: c})
	return nil
}

func (l *Lexer) emitLine(blank bool, off int) error {
	l.began = false
	l.mode = S0
	if l.sink != nil {
		return l.sink.Line(blank, off)
	}
	l.evs = append(l.evs, Ev{Blank: blank, Off: off})
	return nil
}

func (l *Lexer) run(p []byte) error {
	for i := 0; i < len(p) && l.dead == nil; i++ {
		b, o := p[i], l.off
		l.off, l.bytes = l.off+1, l.bytes+1
		switch l.mode {
		case S0:
			switch b {
			case ',':
				if err := l.emitField(o, o); err != nil {
					l.dead = err
				}
			case '"':
				l.quoted, l.start, l.mode = true, o, SQ
			case '\r':
				l.start, l.mode = o, CRU
			case '\n':
				if err := l.emitLine(true, o); err != nil {
					l.dead = err
				}
			default:
				l.start = o
				if !l.put(b) {
					continue
				}
				l.mode = SU
			}
		case SU:
			switch b {
			case ',':
				if err := l.emitField(l.start, o); err != nil {
					l.dead = err
				}
			case '\r':
				l.mode = CRU
			case '\n':
				if err := l.emitField(l.start, o); err == nil {
					err = l.emitLine(false, o)
				}
				if err != nil {
					l.dead = err
				}
			case '"':
				l.fail(ErrBareQuote, o)
			default:
				l.put(b)
			}
		case SQ:
			switch b {
			case '"':
				l.mode = SQP
			case '\r':
				if l.put(b) {
					l.mode = CRQ
				}
			default:
				l.put(b)
			}
		case SQP:
			switch b {
			case '"':
				if l.put(b) {
					l.mode = SQ
				}
			case ',':
				if err := l.emitField(l.start, o); err != nil {
					l.dead = err
				}
			default:
				l.fail(ErrQuoteClosed, o)
			}
		case CRU:
			if b == '\n' {
				if err := l.emitField(l.start, o-1); err == nil {
					err = l.emitLine(false, o)
				}
				if err != nil {
					l.dead = err
				}
			} else {
				l.fail(ErrBareCR, o-1)
			}
		case CRQ:
			switch b {
			case '\n':
				l.put(b)
				l.mode = SQ
			case '"':
				l.mode = SQP
			case '\r':
				if l.put(b) {
					l.mode = CRQ
				}
			default:
				if l.put(b) {
					l.mode = SQ
				}
			}
		}
	}
	if pe, ok := l.dead.(*PosError); ok {
		return pe
	}
	return l.dead
}
