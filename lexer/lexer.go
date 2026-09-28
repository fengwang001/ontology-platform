// Package lexer 是可暂停续传的 CSV 逐字节状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 四类语法错误与孤立 CR，彼此可判定。
var (
	ErrBareQuote        = errors.New("lexer: bare '\"' in unquoted field")
	ErrQuoteAfterClose  = errors.New("lexer: unexpected char after closing quote")
	ErrUnclosedQuote    = errors.New("lexer: unterminated quoted field")
	ErrBareCR           = errors.New("lexer: bare '\\r' not followed by '\\n'")
	ErrFieldTooLong     = errors.New("lexer: field exceeds MaxFieldBytes")
	ErrTooManyFields    = errors.New("lexer: record exceeds MaxFields")
)

// Error 携带出错位置：字节偏移、记录号、字段号（均从 1 起，偏移从 0 起）。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// At 构造带位置的词法错误。
func At(err error, offset, record, field int) *Error {
	return &Error{Err: err, Offset: offset, Record: record, Field: field}
}

// Handler 接收词法事件；table.assembler 与 par.recorder 实现它。
// 所有偏移均为全局字节偏移。
type Handler interface {
	BeginField(offset int, quoted bool)
	Content(b byte)
	EndField(offset int)
	EndRecord(offset, lineNo int, blank bool)
}

// Limits 为可配置上限；零值表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
}

const (
	stStart = iota // 字段开始，未触内容
	stBare         // 未引号字段中
	stQuote        // 引号字段中
	stQSeen        // 引号字段中刚见到一个引号
	stCR           // 行尾 CR 待定
)

// Lexer 是流式状态机。单次使用不保证并发安全。
type Lexer struct {
	h       Handler
	base    int
	off     int
	state   int
	rec     int
	field   int
	touched bool
	quoted  bool
	flen    int
	lim     Limits
	maxf    int
	// cur 汇总当前未结束字段，供 par 取 epilogue 快照。
	curStart int
	buf      []byte
	fatal    *Error
}

// New 创建词法器。base 为本段首字节的全局偏移；insideQuote 为真时
// 以“起点已在引号字段内”的假设启动（par 双假设用）。
func New(h Handler, base int, insideQuote bool, lim Limits, maxFieldsHint int) *Lexer {
	l := &Lexer{h: h, base: base, lim: lim, maxf: maxFieldsHint, rec: 1, field: 1,
		curStart: base}
	if insideQuote {
		l.state = stQuote
		l.quoted = true
		l.touched = true
	}
	return l
}

// Bytes 报告状态机处理过的字节总数（每字节恰好一次）。
func (l *Lexer) Bytes() int { return l.off }

// Fatal 返回终态错误（无则 nil）。
func (l *Lexer) Fatal() *Error { return l.fatal }

func (l *Lexer) fail(err error, pos int) *Error {
	if l.fatal == nil {
		l.fatal = At(err, pos, l.rec, l.field)
	}
	return l.fatal
}

func (l *Lexer) beginField(pos int, quoted bool) {
	l.touched = true
	l.quoted = quoted
	l.curStart = pos
	l.h.BeginField(pos, quoted)
}

func (l *Lexer) endRecord(pos int) {
	l.h.EndField(pos)
	l.h.EndRecord(pos, l.rec, !l.touched)
	l.rec++
	l.field = 1
	l.touched = false
	l.quoted = false
	l.flen = 0
	l.curStart = pos
	l.state = stStart
}

func (l *Lexer) comma(pos int) {
	if l.lim.MaxFields > 0 && l.field >= l.lim.MaxFields {
		l.fail(ErrTooManyFields, pos)
		return
	}
	l.h.EndField(pos)
	l.field++
	l.touched = false
	l.quoted = false
	l.flen = 0
	l.curStart = pos
	l.state = stStart
}

func (l *Lexer) contentByte(b byte, pos int) {
	if l.lim.MaxFieldBytes > 0 && l.flen >= l.lim.MaxFieldBytes {
		l.fail(ErrFieldTooLong, pos)
		return
	}
	l.flen++
	l.h.Content(b)
}

// Feed 送入一段字节，可调用任意次。
func (l *Lexer) Feed(p []byte) error {
	if l.fatal != nil {
		return l.fatal
	}
	for _, b := range p {
		pos := l.base + l.off
		l.off++
		switch l.state {
		case stStart:
			switch b {
			case ',':
				l.comma(pos)
			case '"':
				l.beginField(pos, true)
				l.state = stQuote
			case '\n':
				l.endRecord(pos + 1)
			case '\r':
				l.state = stCR
			default:
				l.beginField(pos, false)
				l.state = stBare
				l.contentByte(b, pos)
			}
		case stBare:
			switch b {
			case ',':
				l.comma(pos)
			case '"':
				l.fail(ErrBareQuote, pos)
			case '\n':
				l.endRecord(pos + 1)
			case '\r':
				l.state = stCR
			default:
				l.contentByte(b, pos)
			}
		case stQuote:
			switch b {
			case '"':
				l.state = stQSeen
			default:
				l.contentByte(b, pos)
			}
		case stQSeen:
			switch b {
			case ',':
				l.comma(pos)
			case '"':
				l.state = stQuote
				l.contentByte('"', pos)
			case '\n':
				l.endRecord(pos + 1)
			case '\r':
				l.state = stCR
			default:
				l.fail(ErrQuoteAfterClose, pos)
			}
		case stCR:
			if b == '\n' {
				l.endRecord(pos + 1)
			} else {
				l.fail(ErrBareCR, pos-1)
			}
		}
		if l.fatal != nil {
			return l.fatal
		}
	}
	return nil
}
