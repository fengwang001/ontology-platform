// Package lexer 是逐字节、可暂停续传的 CSV 状态机。它只把结构事件发给
// Sink；事件带全局字节偏移，可录制重放（par 双假设用）。
package lexer

import "errors"

const (
	stFS = iota // 字段开始
	stUQ        // 未引号字段中
	stQ         // 引号字段中
	stQP        // 引号字段中刚见引号
	stCR        // 行尾 CR 待定
)

// 事件类型。EvRun 标记一段原文内容起点，内容到下一个事件偏移止。
const (
	EvStart      = iota // 字段开始，off=起始偏移
	EvQuoteOpen         // 开引号，off=引号偏移
	EvQuoteClose        // 闭引号，off=闭引号偏移
	EvRun               // 原文内容段 [off, 下一事件 off)；引号态含字段内 \r\n
	EvQChar             // "" 解出的引号字符，off=第一个引号偏移（内容占 2 字节）
	EvFieldEnd          // 字段结束（逗号），off=逗号偏移
	EvRecEnd            // 记录结束，off=\n 偏移
	EvBlank             // 空行丢弃，off=\n 偏移
	EvLoneCR            // 孤立 \r，off=该 \r 偏移
	EvFlush             // EOF：挂起字段落盘（无未闭合引号）
	EvUnclosed          // EOF 引号未闭合，off=起始引号偏移
)

// Ev 是结构事件（8 字节）。
type Ev struct {
	Off int
	K   byte
}

// Sink 接收词法事件。
type Sink interface{ Event(Ev) }

// 可判定哨兵错误。
var (
	ErrBareQuote       = errors.New("lexer: bare '\"' in unquoted field")
	ErrQuoteAfterClose = errors.New("lexer: characters after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unterminated quoted field")
	ErrLoneCR          = errors.New("lexer: bare carriage return")
)

// Lexer 是增量状态机；单实例非并发安全。
type Lexer struct {
	st       int
	openQ    int  // 当前引号字段开引号偏移
	lastQ    int  // stQP/stCR 前最近的引号偏移
	pending  int  // 挂起 \r 偏移
	touched  bool // 当前记录是否触过字节
	err      error
	sink     Sink
	bytesCnt int // 字节处理总次数（每字节恰一次）
}

// New 构造事件接收者为 sink 的状态机。
func New(sink Sink) *Lexer { return &Lexer{st: stFS, lastQ: -1, sink: sink} }

// BytesProcessed 返回已处理字节总数。
func (l *Lexer) BytesProcessed() int { return l.bytesCnt }

// Err 返回终态错误。
func (l *Lexer) Err() error { return l.err }

func (l *Lexer) emit(k byte, off int) { l.sink.Event(Ev{K: k, Off: off}) }

func (l *Lexer) startField(off int) {
	l.touched = true
	l.emit(EvStart, off)
}

// Feed 喂入全局偏移 [base, base+len(p)) 的一段字节，可调用任意多次。
func (l *Lexer) Feed(p []byte, base int) error {
	if l.err != nil {
		return l.err
	}
	for i, c := range p {
		off := base + i
		l.bytesCnt++
		switch l.st {
		case stFS:
			switch c {
			case '"':
				l.startField(off)
				l.emit(EvQuoteOpen, off)
				l.openQ = off
				l.st = stQ
			case ',':
				l.startField(off)
				l.emit(EvFieldEnd, off)
			case '\n':
				l.emit(EvBlank, off)
			case '\r':
				l.pending = off
				l.st = stCR
			default:
				l.startField(off)
				l.emit(EvRun, off)
				l.st = stUQ
			}
		case stUQ:
			switch c {
			case '"':
				l.err = ErrBareQuote
				return l.err
			case ',':
				l.emit(EvFieldEnd, off)
				l.st = stFS
			case '\n':
				l.emit(EvRecEnd, off)
				l.touched = false
				l.st = stFS
			case '\r':
				l.pending = off
				l.st = stCR
			default:
				l.emit(EvRun, off)
			}
		case stQ:
			if c == '"' {
				l.lastQ = off
				l.st = stQP
			} else {
				l.emit(EvRun, off)
			}
		case stQP:
			switch c {
			case '"':
				l.emit(EvQChar, l.lastQ)
				l.st = stQ
			case ',':
				l.emit(EvQuoteClose, l.lastQ)
				l.emit(EvFieldEnd, off)
				l.lastQ = -1
				l.st = stFS
			case '\n':
				l.emit(EvQuoteClose, l.lastQ)
				l.emit(EvRecEnd, off)
				l.touched = false
				l.lastQ = -1
				l.st = stFS
			case '\r':
				l.pending = off
				l.st = stCR
			default:
				l.err = ErrQuoteAfterClose
				return l.err
			}
		case stCR:
			if c == '\n' {
				if l.lastQ >= 0 {
					l.emit(EvQuoteClose, l.lastQ)
					l.lastQ = -1
				}
				l.emit(EvRecEnd, off)
				l.touched = false
				l.st = stFS
				continue
			}
			l.emit(EvLoneCR, l.pending)
			l.err = ErrLoneCR
			return l.err
		}
	}
	return nil
}

// Close 结束流并处理 EOF 语义。
func (l *Lexer) Close() error {
	if l.err != nil {
		return l.err
	}
	switch l.st {
	case stFS:
		if l.touched {
			l.emit(EvFlush, -1)
		}
	case stCR:
		l.emit(EvLoneCR, l.pending)
		l.err = ErrLoneCR
	case stQ:
		l.emit(EvUnclosed, l.openQ)
		l.err = ErrUnclosedQuote
	default: // stUQ, stQP
		if l.st == stQP {
			l.emit(EvQuoteClose, l.lastQ)
		}
		l.emit(EvFlush, -1)
	}
	return l.err
}
