// Package lexer 是逐字节 CSV 状态机：可暂停续传，也可按段双假设运行。
package lexer

import "errors"

// 四类彼此可区分的词法错误（另加上限错误）。
var (
	ErrBareQuote    = errors.New("csv: bare '\"' in unquoted field")
	ErrExtraQuote   = errors.New("csv: unexpected char after closing quote")
	ErrUnterminated = errors.New("csv: unterminated quoted field")
	ErrDanglingCR   = errors.New("csv: bare carriage return not followed by newline")
	ErrFieldTooLong = errors.New("csv: field exceeds MaxFieldBytes")
)

// LexError 携带字节偏移（从 0 起）。
type LexError struct {
	Kind error
	Off  int
}

func (e *LexError) Error() string { return e.Kind.Error() }
func (e *LexError) Unwrap() error { return e.Kind }

// Field 是一个字段片段事件。
type Field struct {
	Value  string
	Quoted bool
	Start  int // 绝对字节偏移
	End    int // 绝对字节偏移（字段末字节之后）
}

// Rec 标记一条记录结束。
type Rec struct{}

// Event 是片段事件：*Field 或 Rec。
type Event any

// Sink 接收词法事件。
type Sink interface {
	Field(f Field) error
	EndRecord() error
}

// Lexer 是流式、可半包续传的状态机；单实例非并发安全。
type Lexer struct {
	m *machine
}

// New 创建流式词法器。base 为输入起始绝对偏移（通常 0）。
func New(sink Sink, maxFieldBytes int, base int) *Lexer {
	return &Lexer{m: newMachine(sink, maxFieldBytes, base, false, 0)}
}

// Processed 返回该状态机处理过的字节总数。
func (l *Lexer) Processed() uint64 { return l.m.processed }

// Feed 续传一段输入，可调用任意次。
func (l *Lexer) Feed(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	return l.m.run(p, false)
}

// Close 宣告流结束。
func (l *Lexer) Close() error { return l.m.run(nil, true) }

// EndMode 描述段结束时的机内状态，供 par 选择接续假设。
type EndMode int

const (
	EndBoundary EndMode = iota // 停在字段边界
	EndBare                    // 停在未引号字段中
	EndQuote                   // 停在引号字段内
	EndQSeen                   // 停在关闭引号之后（逗号/换行之前）
	EndCR                      // 停在 \r 待定
)

// SegResult 是一段输入的双假设解析结果。
type SegResult struct {
	Outside Events // 段首在引号外
	Quoted  Events // 段首已在引号内
	End     EndMode
	EndCR   bool // End==EndCR 时，CR 前引号是否已闭合
	CROff   int  // End==EndCR 时，CR 的绝对偏移
	OutErr  *LexError
	QErr    *LexError
}

// Events 是事件序列。
type Events []Event

// ParseSegment 按两种段首假设并行无关地解析 [start,start+len(p))。
// 每台机器处理字节数计入各自计数；返回结果供上层选择。
func ParseSegment(p []byte, start, maxFieldBytes int) SegResult {
	var r SegResult
	var outS, qS sliceSink
	om := newMachine(&outS, maxFieldBytes, start, false, 0)
	qm := newMachine(&qS, maxFieldBytes, start, true, start)
	if err := om.run(p, false); err != nil {
		r.OutErr = err.(*LexError)
	}
	if err := qm.run(p, false); err != nil {
		r.QErr = err.(*LexError)
	}
	r.Outside = Events(outS)
	r.Quoted = Events(qS)
	r.End = om.endMode()
	r.EndCR = om.crClosed
	r.CROff = om.crOff
	return r
}

// ProcessedTotal 返回段双假设累计处理字节数（供复杂度断言）。
func ProcessedCounted() uint64 { return segProcessed.Load() }

type sliceSink []Event

func (s *sliceSink) Field(f Field) error { *s = append(*s, f); return nil }
func (s *sliceSink) EndRecord() error    { *s = append(*s, Rec{}); return nil }
