// Package lexer 是可暂停续传的 CSV（RFC4180 方言）逐字节状态机。
package lexer

import (
	"errors"
	"fmt"
)

// 四类语法错误哨兵与字段上限哨兵，均可用 errors.Is 判定。
var (
	ErrQuoteInField = errors.New("bare '\"' in unquoted field")
	ErrAfterQuote   = errors.New("unexpected char after closing quote")
	ErrUnclosed     = errors.New("unterminated quoted field")
	ErrBareCR       = errors.New("lone '\\r' not followed by '\\n'")
	ErrFieldTooBig  = errors.New("field exceeds MaxFieldBytes")
)

// Error 携带字节偏移（从 0 起）、记录号、字段号（从 1 起）。
type Error struct {
	Err           error
	Offset        int
	Record, Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

// Limits 为 0 的项表示不限。
type Limits struct{ MaxFieldBytes int }

const (
	KindField uint8 = iota // 字段闭合
	KindRec                // 记录闭合
)

// Ev 是词法事件。字段事件给出解码值、引号标记、原文 [Start,End)、
// 所属记录号与字段号（从 1）；记录事件 RecAt 为行尾最后一个字节偏移+1。
type Ev struct {
	Kind              uint8
	Value             string
	Quoted            bool
	Start, End, RecAt int
	Record, Field     int
}

// StartMode 描述一段解析在其首字节处的真实词法状态。
type StartMode int

const (
	StartAtField  StartMode = iota // 字段起点（引号外，无内容）
	StartInBare                    // 未引号字段中间
	StartInQuoted                  // 引号字段中间（普通态，非刚见引号）
	StartQSeen                     // 引号字段中间且上一字节是引号
)

// Result 是一段独立解析的产物。
type Result struct {
	Ev                 []Ev
	Inside             bool   // 段结束时仍在引号字段内
	QSeen              bool   // Inside 且最后一个字符是引号
	InBare             bool   // 段结束在未引号字段中间
	CRBlank            bool   // 段结束在 CR 待定且为空行情形
	OpenStart          int    // 字段跨段时的全局起点
	OpenVal            string // 跨段字段已解码的段内前缀
	OpenRec, OpenField int    // 跨段字段的记录/字段号
	OpenQuoted         bool
	Err                *Error
	Bytes              int64
}

const (
	stStart = iota
	stUnq
	stQ
	stQSeen
	stCR
)

type machine struct {
	state             int
	val               []byte
	quoted, blank     bool
	begin, pos, crOff int
	nrec, nfield      int
	lim               Limits
	out               []Ev
	processed         int64
	err               *Error
}

// RunSegment 解析一段。base 为首字节全局偏移；mode 为起点词法状态；
// rec0/field0 为该段首字段所属的记录号与字段号（0 基，仅用于事件编号）；
// openVal/openStart 给出跨段接续字段的前缀；final 表示流末尾，需做 EOF 判定。
func RunSegment(buf []byte, base int, mode StartMode, rec0, field0 int,
	openVal string, openStart int, final bool, lim Limits) Result {
	m := &machine{state: stStart, begin: base, pos: base, nrec: rec0, nfield: field0, lim: lim}
	switch mode {
	case StartInBare:
		m.state, m.val, m.begin = stUnq, append([]byte(nil), openVal...), openStart
		m.nfield = field0 - 1
	case StartInQuoted:
		m.state, m.quoted, m.val, m.begin = stQ, true, append([]byte(nil), openVal...), openStart
		m.nfield = field0 - 1
	case StartQSeen:
		m.state, m.quoted, m.val, m.begin = stQSeen, true, append([]byte(nil), openVal...), openStart
		m.nfield = field0 - 1
	}
	for i := 0; i < len(buf); i++ {
		m.processed++
		off := base + i
		m.pos = off + 1
		if m.step(buf[i], off) {
			break
		}
	}
	if m.err == nil && final {
		m.eof()
	}
	r := Result{Ev: m.out, Err: m.err, Bytes: m.processed, OpenStart: m.begin,
		OpenVal: string(m.val), OpenRec: m.nrec + 1, OpenField: m.nfield + 1, OpenQuoted: m.quoted}
	r.Inside = m.state == stQ || m.state == stQSeen
	r.QSeen = m.state == stQSeen
	r.InBare = m.state == stUnq
	r.CRBlank = m.state == stCR && m.blank
	return r
}

func (m *machine) fail(err error, off int) bool {
	m.err = &Error{Err: err, Offset: off, Record: m.nrec + 1, Field: m.nfield + 1}
	return true
}

func (m *machine) add(b byte, off int) bool {
	m.val = append(m.val, b)
	if m.lim.MaxFieldBytes > 0 && len(m.val) > m.lim.MaxFieldBytes {
		return m.fail(ErrFieldTooBig, off)
	}
	return false
}

func (m *machine) emitField(off int) {
	m.out = append(m.out, Ev{Kind: KindField, Value: string(m.val), Quoted: m.quoted,
		Start: m.begin, End: off, Record: m.nrec + 1, Field: m.nfield + 1})
	m.val, m.quoted = nil, false
	m.nfield++
}

func (m *machine) rec(off int) {
	m.out = append(m.out, Ev{Kind: KindRec, RecAt: off, Record: m.nrec + 1, Field: m.nfield})
	m.nrec++
	m.nfield = 0
	m.begin, m.blank, m.state = m.pos, false, stStart
}

func (m *machine) sep(off int) { m.begin, m.state = m.pos, stStart }

func (m *machine) step(b byte, off int) bool {
	switch m.state {
	case stStart:
		switch b {
		case ',':
			m.emitField(off)
			m.sep(off)
		case '"':
			m.quoted, m.state = true, stQ
		case '\n':
			if m.nfield == 0 {
				m.begin = m.pos // 空行跳过
			} else {
				m.emitField(off)
				m.rec(off)
			}
		case '\r':
			if m.nfield > 0 {
				m.emitField(off)
			}
			m.blank, m.crOff, m.state = m.nfield == 0, off, stCR
		default:
			if m.add(b, off) {
				return true
			}
			m.state = stUnq
		}
	case stUnq:
		switch b {
		case ',':
			m.emitField(off)
			m.sep(off)
		case '"':
			return m.fail(ErrQuoteInField, off)
		case '\n':
			m.emitField(off)
			m.rec(off)
		case '\r':
			m.emitField(off)
			m.crOff, m.state = off, stCR
		default:
			if m.add(b, off) {
				return true
			}
		}
	case stQ:
		if b == '"' {
			m.state = stQSeen
		} else if m.add(b, off) {
			return true
		}
	case stQSeen:
		switch b {
		case '"':
			m.state = stQ
			if m.add('"', off) {
				return true
			}
		case ',':
			m.emitField(off + 1)
			m.sep(off)
		case '\n':
			m.emitField(off + 1)
			m.rec(off)
		case '\r':
			m.emitField(off)
			m.crOff, m.state = off, stCR
		default:
			return m.fail(ErrAfterQuote, off)
		}
	case stCR:
		if b == '\n' {
			if m.blank {
				m.state, m.begin, m.blank = stStart, m.pos, false
			} else {
				m.rec(off)
			}
		} else {
			return m.fail(ErrBareCR, m.crOff)
		}
	}
	return false
}

func (m *machine) eof() {
	switch m.state {
	case stQ:
		m.fail(ErrUnclosed, m.begin)
	case stCR:
		m.fail(ErrBareCR, m.crOff)
	case stQSeen:
		m.emitField(m.pos)
		m.rec(m.pos)
	case stUnq:
		m.emitField(m.pos)
		m.rec(m.pos)
	case stStart:
		if m.nfield > 0 {
			m.emitField(m.pos)
			m.rec(m.pos)
		}
	}
}

// Stream 是半包续传流式词法器；单个实例非并发安全。
type Stream struct {
	lim    Limits
	sink   func(Ev)
	err    *Error
	closed bool
	m      machine
}

// NewStream 创建流式词法器，事件经 sink 即时回调。
func NewStream(lim Limits, sink func(Ev)) *Stream {
	return &Stream{lim: lim, sink: sink, m: machine{state: stStart, begin: 0}}
}

// Bytes 返回状态机累计处理字节数（每字节恰好一次）。
func (s *Stream) Bytes() int64 { return s.m.processed }

// Feed 追加一段字节；进入终态后恒返回同一个错误。
func (s *Stream) Feed(p []byte) error {
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return s.err
	}
	s.m.out = s.m.out[:0]
	for i := 0; i < len(p); i++ {
		s.m.processed++
		off := s.m.pos + i
		s.m.pos = off + 1
		if s.m.step(p[i], off) {
			break
		}
	}
	for _, e := range s.m.out {
		s.sink(e)
	}
	// 终态错误固化
	s.err = s.m.err
	return s.err
}

// Close 声明流结束并做 EOF 判定；终态后返回同一错误。
func (s *Stream) Close() error {
	if s.err != nil {
		return s.err
	}
	s.closed = true
	s.m.out = s.m.out[:0]
	s.m.eof()
	for _, e := range s.m.out {
		s.sink(e)
	}
	s.err = s.m.err
	return s.err
}
