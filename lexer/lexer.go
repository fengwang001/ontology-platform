package lexer

import "ontology/cell"

type State int

const (
	StStart State = iota // 字段开始（引号外）
	StUnquoted
	StQuoted
	StQSeen
	StCR // 行尾 CR 待定
)

type Kind int

const (
	ErrBareQuote Kind = iota + 1 // 未引号字段中出现 "
	ErrAfterQuote                // 引号闭合后紧跟非法字符
	ErrUnclosed                  // 流结束引号未闭合
	ErrBareCR                    // 孤立 \r
	ErrFieldTooLong
	ErrTooManyFields
	ErrTooManyRecords
	ErrFieldCount
)

// Error 携带字节偏移(从0)、记录号、字段号(均从1)。
type Error struct {
	Kind          Kind
	Offset        int
	Record, Field int
}

func (e *Error) Error() string { return "csv lexer error" }

type Limits struct {
	MaxFieldBytes int // 0 表示不限
	MaxFields     int
}

type Sink interface {
	Field(cell.Cell)
	EndRecord()
	Error(*Error)
}

type Lexer struct {
	sink  Sink
	lim   Limits
	base  int // 本段首字节的全局偏移
	recNo int // 下一条记录号（1 起）

	state   State
	active  bool   // 当前字段已开始
	pending bool   // 逗号后期待新字段
	quoted  bool
	val     []byte
	start   int
	end     int
	fieldNo int
	seen    bool
	crOff   int
	pos     int

	processed int64
	terminal  *Error
}

func New(sink Sink, lim Limits) *Lexer {
	return &Lexer{sink: sink, lim: lim, recNo: 1}
}

// NewAt 供 par 使用：以已知全局坐标与起始状态构造。
func NewAt(sink Sink, lim Limits, base, recNo int, st State, active bool) *Lexer {
	l := New(sink, lim)
	l.base, l.recNo, l.state, l.active = base, recNo, st, active
	l.seen = true // 段内不再判断"空文件"
	return l
}

func (l *Lexer) Processed() int64 { return l.processed }
func (l *Lexer) State() State     { return l.state }
func (l *Lexer) Active() bool     { return l.active }
func (l *Lexer) Records() int     { return l.recNo - 1 }
func (l *Lexer) Terminal() *Error { return l.terminal }

func (l *Lexer) fail(k Kind, off int) {
	l.terminal = &Error{Kind: k, Offset: off, Record: l.recNo, Field: l.fieldNo}
	l.sink.Error(l.terminal)
}

func (l *Lexer) emit(off int) {
	if l.lim.MaxFields > 0 && l.fieldNo > l.lim.MaxFields {
		l.fail(ErrTooManyFields, off)
		return
	}
	l.sink.Field(cell.Cell{Value: l.val, Quoted: l.quoted, Start: l.start, End: l.end})
	l.val, l.active, l.quoted = nil, false, false
}

// begin 在 StStart 每个字节处调用：该字节总是开启一个新字段。
func (l *Lexer) begin(off int) bool {
	l.fieldNo++
	l.start, l.end, l.pending = off, off, false
	if l.lim.MaxFields > 0 && l.fieldNo > l.lim.MaxFields {
		l.fail(ErrTooManyFields, off)
		return false
	}
	return true
}

func (l *Lexer) endRecord() {
	l.sink.EndRecord()
	l.recNo++
	l.fieldNo, l.pending = 0, false
}

func (l *Lexer) add(b byte, off int) bool {
	if l.lim.MaxFieldBytes > 0 && len(l.val)+1 > l.lim.MaxFieldBytes {
		l.fail(ErrFieldTooLong, off)
		return false
	}
	l.val = append(l.val, b)
	return true
}

func (l *Lexer) Feed(p []byte) error {
	if l.terminal != nil {
		return l.terminal
	}
	for i := 0; i < len(p); i++ {
		l.processed++
		off := l.base + i
		b := p[i]
		l.seen = true
		l.pos = off + 1
		switch l.state {
		case StStart:
			if !l.begin(off) {
				return l.terminal
			}
			switch b {
			case ',':
				l.sink.Field(cell.Cell{Start: off, End: off})
				l.pending = true
			case '"':
				l.active, l.quoted, l.state = true, true, StQuoted
			case '\r':
				l.state, l.crOff = StCR, off
			case '\n':
				l.sink.Field(cell.Cell{Start: off, End: off})
				l.endRecord()
			default:
				l.active = true
				if !l.add(b, off) {
					return l.terminal
				}
				l.end = off + 1
				l.state = StUnquoted
			}
		case StUnquoted:
			switch b {
			case ',':
				l.emit(off)
				if l.terminal != nil {
					return l.terminal
				}
				l.pending = true
			case '"':
				l.fail(ErrBareQuote, off)
			case '\r':
				l.state, l.crOff = StCR, off
			case '\n':
				l.emit(off)
				if l.terminal == nil {
					l.endRecord()
				}
			default:
				if !l.add(b, off) {
					return l.terminal
				}
				l.end = off + 1
			}
		case StQuoted:
			if b == '"' {
				l.state = StQSeen
				l.end = off + 1
			} else {
				if !l.add(b, off) {
					return l.terminal
				}
				l.end = off + 1
			}
		case StQSeen:
			switch b {
			case ',':
				l.emit(off)
				if l.terminal != nil {
					return l.terminal
				}
				l.pending = true
			case '"':
				l.state = StQuoted
				if !l.add('"', off) {
					return l.terminal
				}
				l.end = off + 1
			case '\r':
				l.state, l.crOff = StCR, off
			case '\n':
				l.emit(off)
				if l.terminal == nil {
					l.endRecord()
				}
			default:
				l.fail(ErrAfterQuote, off)
			}
		case StCR:
			if b == '\n' {
				if l.active {
					l.emit(off)
				} else {
					l.fieldNo++
					l.sink.Field(cell.Cell{Start: l.crOff, End: l.crOff})
				}
				if l.terminal == nil {
					l.endRecord()
					l.state = StStart
				}
			} else {
				l.fail(ErrBareCR, l.crOff)
			}
		}
		if l.terminal != nil {
			return l.terminal
		}
	}
	return nil
}

func (l *Lexer) Close() error {
	if l.terminal != nil {
		return l.terminal
	}
	switch l.state {
	case StQuoted:
		l.fail(ErrUnclosed, l.start)
	case StCR:
		l.fail(ErrBareCR, l.crOff)
	default:
		if !l.seen {
			return nil
		}
		if l.active {
			l.emit(l.pos)
		} else if l.pending {
			l.begin(l.pos)
			l.emit(l.pos)
		} else {
			return nil
		}
		if l.terminal == nil {
			l.endRecord()
		}
	}
	return l.terminal
}
