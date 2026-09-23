// Package lexer 是可暂停/续传的逐字节 CSV 状态机，通过 Sink 吐出字段事件。
package lexer

import "errors"

// 语法错误与字段级上限（记录列数错误由 table 包定义）。
var (
	ErrQuoteInBare     = errors.New("bare field contains quote")
	ErrCharsAfterQuote = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("unterminated quoted field")
	ErrLoneCR          = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLong    = errors.New("field exceeds MaxFieldBytes")
	ErrTooManyFields   = errors.New("record exceeds MaxFieldsPerRecord")
)

// Limits 为可配置上限，0 表示不限。
type Limits struct {
	MaxFieldBytes      int
	MaxFieldsPerRecord int
	MaxRecords         int
}

// Error 携带出错坐标：字节偏移从 0 起，记录号、字段号从 1 起（段内坐标）。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Sink 接收状态机事件；Data 的切片只在调用期间有效。
type Sink interface {
	CellStart(off int) error
	Data(p []byte) error
	EscapedQuote(off int) error
	CellEnd(off int, quoted bool) error
	RecordEnd(lfOff int) error
}

const (
	stStart = iota
	stBare
	stQuoted
	stQAfter
	stCR
)

// Lexer 单实例非并发安全。
type Lexer struct {
	sink      Sink
	lim       Limits
	state     int
	off       int
	cellStart int
	begun     bool
	recNo     int
	fields    int
	quoted    bool
	fbytes    int
	crOff     int
	finalErr  error
	byteCount int64
}

// New 创建从引号外、字段开始处启动的状态机。
func New(sink Sink, lim Limits) *Lexer {
	return &Lexer{sink: sink, lim: lim, state: stStart}
}

// NewResumeQuoted 创建假设「起点已在引号字段中段」的状态机（供 par）。
func NewResumeQuoted(sink Sink, lim Limits) *Lexer {
	l := New(sink, lim)
	l.state, l.begun, l.fields, l.quoted = stQuoted, true, 1, true
	return l
}

// InQuoted 报告处理完本段后是否停在引号字段内部（供 par 选假设）。
func (l *Lexer) InQuoted() bool { return l.state == stQuoted }

// PendingCR 报告是否停在未引号字段末尾的 CR 待定状态。
func (l *Lexer) PendingCR() bool { return l.state == stCR }

// ByteCount 返回状态机处理过的字节总数（PeekNext 的窥视不计）。
func (l *Lexer) ByteCount() int64 { return l.byteCount }

// FinalErr 返回终态错误（nil 表示尚未出错）。
func (l *Lexer) FinalErr() error { return l.finalErr }

func (l *Lexer) fail(off int, err error) error {
	if l.finalErr == nil {
		l.finalErr = &Error{Err: err, Offset: off, Record: l.recNo + 1, Field: l.fields}
	}
	return l.finalErr
}

func (l *Lexer) terminal(err error) error {
	if err != nil && l.finalErr == nil {
		l.finalErr = err
	}
	return l.finalErr
}

func (l *Lexer) begin(off int) error {
	if l.begun {
		return nil
	}
	if l.lim.MaxFieldsPerRecord > 0 && l.fields >= l.lim.MaxFieldsPerRecord {
		return l.fail(off, ErrTooManyFields)
	}
	l.begun, l.cellStart, l.fields = true, off, l.fields+1
	return l.sink.CellStart(off)
}

func (l *Lexer) countByte(off int) error {
	l.fbytes++
	if l.lim.MaxFieldBytes > 0 && l.fbytes > l.lim.MaxFieldBytes {
		return l.fail(off, ErrFieldTooLong)
	}
	return nil
}

func (l *Lexer) raw(off int, b byte) error {
	if err := l.countByte(off); err != nil {
		return err
	}
	return l.sink.Data([]byte{b})
}

func (l *Lexer) endCell(off int) error {
	if !l.begun {
		return nil
	}
	err := l.terminal(l.sink.CellEnd(off, l.quoted))
	l.begun, l.quoted, l.fbytes = false, false, 0
	return err
}

// sep 处理逗号：结束当前字段并回到字段开始状态。
func (l *Lexer) sep(off int) error {
	if err := l.begin(off); err != nil {
		return err
	}
	if err := l.endCell(off); err != nil {
		return err
	}
	l.state = stStart
	return nil
}

// term 处理一条记录在 LF（全局偏移 lfOff）处结束；bareCR 为 true 时该 LF 前有 CR。
func (l *Lexer) term(lfOff int, bareCR bool) error {
	end := lfOff
	if bareCR {
		end = lfOff - 1 // 半开区间：CR 不属于字段，End 指向 CR
	}
	if err := l.endCell(end); err != nil {
		return err
	}
	l.recNo++
	err := l.terminal(l.sink.RecordEnd(lfOff))
	l.fields, l.state = 0, stStart
	return err
}

// PeekNext 仅在 PendingCR 时使用：窥视下段首字节以定案 CR，窥视不占字节计数。
func (l *Lexer) PeekNext(b byte) error {
	if l.finalErr != nil || l.state != stCR {
		return l.finalErr
	}
	if b == '\n' {
		return l.term(l.off+1, true)
	}
	return l.fail(l.crOff, ErrLoneCR)
}

// Feed 喂入任意长度的一段字节，可反复调用。
func (l *Lexer) Feed(p []byte) error {
	if l.finalErr != nil {
		return l.finalErr
	}
	for i := 0; i < len(p); i++ {
		off := l.off
		l.off, l.byteCount = off+1, l.byteCount+1
		b := p[i]
		switch l.state {
		case stStart:
			switch {
			case b == '"':
				if err := l.begin(off); err != nil {
					return err
				}
				l.quoted, l.state = true, stQuoted
			case b == ',':
				if err := l.sep(off); err != nil {
					return err
				}
			case b == '\n':
				if l.fields > 0 {
					if err := l.term(off+1, false); err != nil {
						return err
					}
				}
			case b == '\r':
				if err := l.begin(off); err != nil {
					return err
				}
				l.crOff, l.state = off, stCR
			default:
				if err := l.begin(off); err != nil {
					return err
				}
				if err := l.raw(off, b); err != nil {
					return l.fail(off, err)
				}
				l.state = stBare
			}
		case stBare:
			switch {
			case b == ',':
				if err := l.sep(off); err != nil {
					return err
				}
			case b == '"':
				return l.fail(off, ErrQuoteInBare)
			case b == '\n':
				if err := l.term(off+1, false); err != nil {
					return err
				}
			case b == '\r':
				l.crOff, l.state = off, stCR
			default:
				if err := l.raw(off, b); err != nil {
					return l.fail(off, err)
				}
			}
		case stQuoted:
			if b == '"' {
				l.state = stQAfter
			} else if err := l.raw(off, b); err != nil {
				return l.fail(off, err)
			}
		case stQAfter:
			switch {
			case b == '"':
				if err := l.sink.EscapedQuote(off); err != nil {
					return l.fail(off, err)
				}
				if err := l.countByte(off); err != nil {
					return err
				}
				l.state = stQuoted
			case b == ',':
				if err := l.sep(off); err != nil {
					return err
				}
			case b == '\n':
				if err := l.term(off+1, false); err != nil {
					return err
				}
			case b == '\r':
				l.crOff, l.state = off, stCR
			default:
				return l.fail(off, ErrCharsAfterQuote)
			}
		case stCR:
			if b == '\n' {
				if err := l.term(off+1, true); err != nil {
					return err
				}
			} else {
				return l.fail(l.crOff, ErrLoneCR)
			}
		}
	}
	return nil
}

// Close 宣布流结束，处理未决记录或未闭合引号。
func (l *Lexer) Close() error {
	if l.finalErr != nil {
		return l.finalErr
	}
	switch l.state {
	case stQuoted:
		return l.fail(l.cellStart, ErrUnclosedQuote)
	case stCR:
		return l.fail(l.crOff, ErrLoneCR)
	default:
		if l.begun {
			if err := l.endCell(l.off); err != nil {
				return err
			}
			l.recNo++
			if err := l.sink.RecordEnd(l.off); err != nil {
				return l.fail(l.off, err)
			}
		}
	}
	return nil
}
