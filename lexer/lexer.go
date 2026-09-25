// Package lexer 是可暂停、可续传的 CSV 逐字节状态机。
package lexer

import "errors"

// Kind 标识可判定错误类别。
type Kind int

const (
	KBadQuote Kind = iota // 未引号字段中出现 "
	KTailQuote            // 引号闭合后紧跟非法字符
	KUnclosed             // 流末引号未闭合
	KLoneCR               // 孤立 \r
	KFieldTooLarge        // 单字段超字节上限
)

var (
	ErrBadQuote     = errors.New("csv: bare '\"' in unquoted field")
	ErrTailQuote    = errors.New("csv: unexpected char after quoted field")
	ErrUnclosed     = errors.New("csv: unterminated quoted field")
	ErrLoneCR       = errors.New("csv: bare '\\r' not followed by '\\n'")
	ErrFieldTooLarge = errors.New("csv: field exceeds max bytes")
	ErrTerminal     = errors.New("csv: parser is in terminal error state")
)

// ErrOf 返回 Kind 对应的哨兵错误。
func ErrOf(k Kind) error {
	return [...]error{ErrBadQuote, ErrTailQuote, ErrUnclosed, ErrLoneCR, ErrFieldTooLarge}[k]
}

// State 是状态机当前状态（供 par 续传假设使用）。
type State int

const (
	SStart State = iota // 字段开始
	SBare               // 未引号字段中
	SQuote              // 引号字段中
	SQClose             // 引号字段中刚见引号
	SCR                 // 行尾 CR 待定
)

// Sink 接收词法事件；偏移均为相对本次喂入起点的字节偏移。
type Sink interface {
	FieldStart(off int)
	FieldByte(b byte)
	FieldQuoted()
	FieldEnd(off int)
	RecordEnd(off int)
	Error(k Kind, off int)
}

// Lexer 为流式状态机。单实例非并发安全。
type Lexer struct {
	sink   Sink
	maxFld int
	state  State
	pos    int  // 已消费字节数
	fsz    int  // 当前字段已计值字节
	open   bool // 当前字段已开始
	rec    bool // 当前记录已有字段开始
	steps  int64
	term   error
}

// New 创建从字段开始状态启动的状态机。maxFieldBytes<=0 表示不限。
func New(sink Sink, maxFieldBytes int) *Lexer {
	return &Lexer{sink: sink, maxFld: maxFieldBytes}
}

// Resume 从指定状态续传：q=true 表示起点位于引号字段内。
func Resume(sink Sink, maxFieldBytes int, q bool) *Lexer {
	l := New(sink, maxFieldBytes)
	if q {
		l.state, l.open, l.rec = SQuote, true, true
	}
	return l
}

// Steps 返回状态机处理过的字节总数（每字节恰好一次）。
func (l *Lexer) Steps() int64 { return l.steps }

// State 返回当前状态。
func (l *Lexer) State() State { return l.state }

func (l *Lexer) fail(k Kind, off int) error {
	l.term = ErrOf(k)
	l.sink.Error(k, off)
	return l.term
}

func (l *Lexer) begin(off int) {
	if l.open {
		return
	}
	l.open, l.rec = true, true
	l.sink.FieldStart(off)
}

func (l *Lexer) put(b byte, off int) error {
	l.fsz++
	if l.maxFld > 0 && l.fsz > l.maxFld {
		return l.fail(KFieldTooLarge, off)
	}
	l.sink.FieldByte(b)
	return nil
}

func (l *Lexer) endField(off int) { l.open = false; l.sink.FieldEnd(off) }

// Feed 消费一段字节；进入终态后返回同一错误（ErrTerminal 包裹之）。
func (l *Lexer) Feed(p []byte) error {
	if l.term != nil {
		return l.term
	}
	for _, b := range p {
		i := l.pos
		l.steps++
		l.pos++
		switch l.state {
		case SStart:
			switch b {
			case ',':
				l.begin(i)
				l.endField(i)
				l.begin(i + 1)
			case '\n':
				if !l.rec {
					break
				}
				l.begin(i)
				l.endField(i)
				l.sink.RecordEnd(i)
				l.open, l.rec = false, false
			case '\r':
				l.begin(i)
				l.state = SCR
			case '"':
				l.begin(i)
				l.sink.FieldQuoted()
				l.state = SQuote
			default:
				l.begin(i)
				if err := l.put(b, i); err != nil {
					return err
				}
				l.state = SBare
			}
		case SBare:
			switch b {
			case ',':
				l.endField(i)
				l.begin(i + 1)
				l.state = SStart
			case '\n':
				l.endField(i)
				l.sink.RecordEnd(i)
				l.open, l.rec, l.state = false, false, SStart
			case '\r':
				l.state = SCR
			case '"':
				return l.fail(KBadQuote, i)
			default:
				if err := l.put(b, i); err != nil {
					return err
				}
			}
		case SQuote:
			if b == '"' {
				l.state = SQClose
			} else {
				if err := l.put(b, i); err != nil {
					return err
				}
			}
		case SQClose:
			switch b {
			case ',':
				l.endField(i)
				l.begin(i + 1)
				l.state = SStart
			case '\n':
				l.endField(i)
				l.sink.RecordEnd(i)
				l.open, l.rec, l.state = false, false, SStart
			case '\r':
				l.state = SCR
			case '"':
				if err := l.put('"', i); err != nil {
					return err
				}
				l.state = SQuote
			default:
				return l.fail(KTailQuote, i)
			}
		case SCR:
			if b != '\n' {
				return l.fail(KLoneCR, i)
			}
			l.endField(i)
			l.sink.RecordEnd(i)
			l.open, l.rec, l.state = false, false, SStart
		}
	}
	return nil
}

// Close 宣告流结束，处理 EOF 语义。
func (l *Lexer) Close() error {
	if l.term != nil {
		return l.term
	}
	n := l.pos
	switch l.state {
	case SQuote:
		return l.fail(KUnclosed, n)
	case SCR:
		return l.fail(KLoneCR, n-1)
	case SStart:
		if !l.rec {
			return nil
		}
		l.begin(n)
		l.endField(n)
		l.sink.RecordEnd(n)
	case SBare, SQClose:
		l.endField(n)
		l.sink.RecordEnd(n)
	}
	l.state = SStart
	l.open, l.rec = false, false
	return nil
}
