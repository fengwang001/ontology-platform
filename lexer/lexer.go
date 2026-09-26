// Package lexer 是可暂停续传的逐字节 CSV 状态机，只依赖 cell。
package lexer

import (
	"errors"
	"strings"

	"ontology/cell"
)

// 四类语法错误与三类上限错误，彼此可用 errors.Is 区分。
var (
	ErrBareQuote     = errors.New("bare '\"' in unquoted field")
	ErrQuoteClosed   = errors.New("unexpected char after closed quote")
	ErrUnclosedQuote = errors.New("unterminated quoted field")
	ErrLoneCR        = errors.New("lone '\\r' not followed by '\\n'")
	ErrFieldTooLong  = errors.New("field exceeds max bytes")
	ErrTooManyFields = errors.New("record exceeds max fields")
	ErrTooManyRecord = errors.New("too many records")
)

// Error 携带字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type Error struct {
	Err            error
	Offset         int
	Record, Field  int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Sink 接收词法事件。
type Sink interface {
	Field(cell.Cell)
	EndRecord()
}

// Limits 为 0 表示不限制。
type Limits struct{ MaxFieldBytes, MaxFieldsPerRecord, MaxRecords int }

// State 是段末状态。
type State int

const (
	StateFieldStart State = iota // F：字段开始（未引号）
	StateBare                    // B：裸字段中
	StateQuote                   // Q：引号字段中
	StateAfterQuote              // A：引号字段中刚见引号
	StateCR                      // C：行尾 CR 待定
)

// Mode 是段起点播种模式。
type Mode int

const (
	ModeStart Mode = iota // H0：引号外字段首
	ModeQuote             // H1：已在引号字段内
	ModeAfterQuote        // 上一段末字节是闭引号
)

// Seed 播种一个从任意字节偏移开始的段解析器。
type Seed struct {
	Base, RecordsBefore, FieldsBefore int
	Mode                              Mode
}

// Stream 是单实例状态机；单实例非并发安全。
type Stream struct {
	sink                                       Sink
	lim                                        Limits
	state                                      int
	off, fstart, fields, records, crOff        int
	buf                                        strings.Builder
	quoted, crRec                              bool
	term                                       error
	handled                                    int64
}

// New 创建流式解析器。
func New(sink Sink, lim Limits) *Stream { return &Stream{sink: sink, lim: lim} }

// NewSegment 创建播种段解析器。
func NewSegment(sink Sink, lim Limits, sd Seed) *Stream {
	s := &Stream{sink: sink, lim: lim, off: sd.Base, fstart: sd.Base,
		records: sd.RecordsBefore, fields: sd.FieldsBefore}
	if sd.Mode == ModeQuote {
		s.state, s.quoted = int(StateQuote), true
	}
	if sd.Mode == ModeAfterQuote {
		s.state, s.quoted = int(StateAfterQuote), true
	}
	return s
}

// Handled 返回状态机处理过的字节总数。
func (s *Stream) Handled() int64 { return s.handled }

// Records 返回已闭合记录数。
func (s *Stream) Records() int { return s.records }

// FieldsInRecord 返回当前记录已发字段数。
func (s *Stream) FieldsInRecord() int { return s.fields }

// State 返回当前状态。
func (s *Stream) State() State { return State(s.state) }

// Pending 返回挂起字段（End=当前偏移）。
func (s *Stream) Pending() cell.Cell {
	return cell.Cell{Value: s.buf.String(), Quoted: s.quoted, Start: s.fstart, End: s.off}
}

func (s *Stream) fail(err error, off int) error {
	e := &Error{Err: err, Offset: off, Record: s.records + 1, Field: s.fields + 1}
	s.term = e
	return e
}

func (s *Stream) put(ch byte) error {
	if s.lim.MaxFieldBytes > 0 && s.buf.Len() >= s.lim.MaxFieldBytes {
		return s.fail(ErrFieldTooLong, s.off)
	}
	s.buf.WriteByte(ch)
	return nil
}

func (s *Stream) emit(end int) error {
	if s.lim.MaxFieldsPerRecord > 0 && s.fields >= s.lim.MaxFieldsPerRecord {
		return s.fail(ErrTooManyFields, end)
	}
	s.sink.Field(cell.Cell{Value: s.buf.String(), Quoted: s.quoted, Start: s.fstart, End: end})
	s.fields++
	s.buf.Reset()
	s.quoted = false
	return nil
}

func (s *Stream) endRec() error {
	if s.lim.MaxRecords > 0 && s.records >= s.lim.MaxRecords {
		return s.fail(ErrTooManyRecord, s.off)
	}
	s.sink.EndRecord()
	s.records++
	s.fields = 0
	s.fstart = s.off + 1
	return nil
}

func (s *Stream) cr(off int, rec bool) { s.state, s.crOff, s.crRec = int(StateCR), off, rec }

// Feed 送入一段字节，可调用任意多次。
func (s *Stream) Feed(p []byte) error {
	if s.term != nil {
		return s.term
	}
	for _, ch := range p {
		s.handled++
		off := s.off
		switch s.state {
		case int(StateFieldStart): // F
			switch ch {
			case ',':
				if e := s.emit(off); e != nil {
					return e
				}
			case '"':
				s.state, s.quoted = int(StateQuote), true
			case '\n':
				if s.fields > 0 {
					if e := s.emit(off); e != nil {
						return e
					}
					if e := s.endRec(); e != nil {
						return e
					}
				}
				s.fstart = off + 1
			case '\r':
				s.cr(off, s.fields > 0)
			default:
				if e := s.put(ch); e != nil {
					return e
				}
				s.state = int(StateBare)
			}
		case int(StateBare): // B
			switch ch {
			case ',':
				if e := s.emit(off); e != nil {
					return e
				}
				s.state, s.fstart = int(StateFieldStart), off+1
			case '"':
				return s.fail(ErrBareQuote, off)
			case '\n':
				if e := s.emit(off); e != nil {
					return e
				}
				if e := s.endRec(); e != nil {
					return e
				}
				s.state = int(StateFieldStart)
			case '\r':
				s.cr(off, true)
			default:
				if e := s.put(ch); e != nil {
					return e
				}
			}
		case int(StateQuote): // Q
			if ch == '"' {
				s.state = int(StateAfterQuote)
			} else if e := s.put(ch); e != nil {
				return e
			}
		case int(StateAfterQuote): // A
			switch ch {
			case ',':
				if e := s.emit(off); e != nil {
					return e
				}
				s.state, s.fstart = int(StateFieldStart), off+1
			case '"':
				if e := s.put('"'); e != nil {
					return e
				}
				s.state = int(StateQuote)
			case '\n':
				if e := s.emit(off); e != nil {
					return e
				}
				if e := s.endRec(); e != nil {
					return e
				}
				s.state = int(StateFieldStart)
			case '\r':
				if e := s.emit(off); e != nil {
					return e
				}
				s.cr(off, true)
			default:
				return s.fail(ErrQuoteClosed, off)
			}
		default: // C
			s.state = int(StateFieldStart)
			if ch != '\n' {
				return s.fail(ErrLoneCR, s.crOff)
			}
			if !s.crRec {
				s.fstart = off + 1
				break
			}
			if s.buf.Len() > 0 {
				if e := s.emit(off - 1); e != nil {
					return e
				}
			}
			if e := s.endRec(); e != nil {
				return e
			}
		}
		s.off++
	}
	return nil
}

// Close 宣告流结束。
func (s *Stream) Close() error {
	if s.term != nil {
		return s.term
	}
	switch State(s.state) {
	case StateQuote:
		return s.fail(ErrUnclosedQuote, s.fstart)
	case StateCR:
		return s.fail(ErrLoneCR, s.crOff)
	case StateAfterQuote:
		if e := s.emit(s.off); e != nil {
			return e
		}
		if e := s.endRec(); e != nil {
			return e
		}
	case StateBare:
		if e := s.emit(s.off); e != nil {
			return e
		}
		if e := s.endRec(); e != nil {
			return e
		}
	case StateFieldStart:
		if s.fields > 0 {
			if e := s.emit(s.off); e != nil {
				return e
			}
			if e := s.endRec(); e != nil {
				return e
			}
		}
	}
	return nil
}
