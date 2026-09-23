// Package lexer 是可暂停续传的逐字节 CSV 状态机，不使用 encoding/csv。
package lexer

import "ontology/cell"

// State 是状态机当前状态。
type State int

const (
	StateN  State = iota // 字段开始（可能在行首）
	StateU               // 未引号字段中
	StateQ               // 引号字段中
	StateQE              // 引号字段中刚见引号
	StateR               // 行尾 CR 待定
)

// EvKind 是事件类别。
type EvKind int

const (
	EvField  EvKind = iota // 一个字段完成
	EvRecord               // 一条记录结束
	EvBlank                // 空行，跳过
)

// Event 是词法事件。
type Event struct {
	Kind EvKind
	Cell cell.Cell
}

// Limits 为词法层上限，0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
}

// ErrKind 区分四类语法错误与两类上限错误。
type ErrKind int

const (
	ErrBareQuote       ErrKind = iota + 1 // 未引号字段中出现 "
	ErrQuoteAfterClose                    // 引号闭合后跟非法字符
	ErrUnclosedQuote                      // 流结束引号未闭合
	ErrBareCR                             // 孤立 \r
	ErrFieldTooLong                       // 单字段超限
	ErrTooManyFields                      // 单记录字段数超限
)

// Error 带字节偏移、记录号、字段号（均从 1 起，偏移从 0 起）。
type Error struct {
	Kind   ErrKind
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return "csv lexer error" }

var (
	sBare = &Error{Kind: ErrBareQuote}
	sQAft = &Error{Kind: ErrQuoteAfterClose}
	sUncl = &Error{Kind: ErrUnclosedQuote}
	sCR   = &Error{Kind: ErrBareCR}
	sLong = &Error{Kind: ErrFieldTooLong}
	sMany = &Error{Kind: ErrTooManyFields}
)

// Sentinel 返回某类错误的哨兵；errors.Is 可识别其 Kind。
func Sentinel(k ErrKind) error {
	switch k {
	case ErrBareQuote:
		return sBare
	case ErrQuoteAfterClose:
		return sQAft
	case ErrUnclosedQuote:
		return sUncl
	case ErrBareCR:
		return sCR
	case ErrFieldTooLong:
		return sLong
	default:
		return sMany
	}
}

// Is 使填充了位置的 *Error 能匹配对应哨兵。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// Run 从给定状态与悬挂字段出发处理 p（base 为 p[0] 全局偏移），
// fieldsBefore 为本记录此前已完成字段数。双假设原语，无回扫。
func Run(start State, pend cell.Cell, lineStart bool, p []byte, base, fieldsBefore int, lim Limits) (evs []Event, end State, out cell.Cell, ls bool, err *Error) {
	s, cur := start, pend
	fno := fieldsBefore
	put := func(c cell.Cell) bool {
		fno++
		if lim.MaxFields > 0 && fno > lim.MaxFields {
			err = &Error{Kind: ErrTooManyFields, Offset: c.End, Field: fno}
			return false
		}
		evs = append(evs, Event{Kind: EvField, Cell: c})
		return true
	}
	grow := func(add string, at int) bool {
		if lim.MaxFieldBytes > 0 && len(cur.Value)+len(add) > lim.MaxFieldBytes {
			err = &Error{Kind: ErrFieldTooLong, Offset: at, Field: fno + 1}
			return false
		}
		cur.Value += add
		return true
	}
	for i, b := range p {
		pos := base + i
		switch s {
		case StateN:
			switch b {
			case ',':
				cur.End = pos
				if !put(cur) {
					return
				}
				cur = cell.Cell{Start: pos + 1, End: pos + 1}
				lineStart = false
			case '\n':
				if lineStart {
					evs = append(evs, Event{Kind: EvBlank})
				} else {
					cur.End = pos
					if !put(cur) {
						return
					}
					evs = append(evs, Event{Kind: EvRecord})
				}
				cur = cell.Cell{Start: pos + 1, End: pos + 1}
				lineStart = true
			case '\r':
				s = StateR
			case '"':
				cur.Quoted = true
				cur.Start = pos
				s = StateQ
			default:
				if !grow(string(b), pos) {
					return
				}
				s = StateU
			}
		case StateU:
			switch b {
			case ',':
				cur.End = pos
				if !put(cur) {
					return
				}
				cur = cell.Cell{Start: pos + 1, End: pos + 1}
				lineStart = false
				s = StateN
			case '\n':
				cur.End = pos
				if !put(cur) {
					return
				}
				evs = append(evs, Event{Kind: EvRecord})
				cur = cell.Cell{Start: pos + 1, End: pos + 1}
				lineStart = true
				s = StateN
			case '\r':
				s = StateR
			case '"':
				err = &Error{Kind: ErrBareQuote, Offset: pos, Field: fno + 1}
				return
			default:
				if !grow(string(b), pos) {
					return
				}
			}
		case StateQ:
			switch b {
			case '"':
				s = StateQE
			default: // 含 , \r \n，全部原样保留
				if !grow(string(b), pos) {
					return
				}
			}
		case StateQE:
			switch b {
			case '"':
				if !grow("\"", pos) {
					return
				}
				s = StateQ
			case ',':
				cur.End = pos
				if !put(cur) {
					return
				}
				cur = cell.Cell{Start: pos + 1, End: pos + 1}
				lineStart = false
				s = StateN
			case '\n':
				cur.End = pos
				if !put(cur) {
					return
				}
				evs = append(evs, Event{Kind: EvRecord})
				cur = cell.Cell{Start: pos + 1, End: pos + 1}
				lineStart = true
				s = StateN
			case '\r':
				s = StateR
			default:
				err = &Error{Kind: ErrQuoteAfterClose, Offset: pos, Field: fno + 1}
				return
			}
		case StateR:
			if b != '\n' {
				err = &Error{Kind: ErrBareCR, Offset: pos - 1, Field: fno + 1}
				return
			}
			if lineStart {
				evs = append(evs, Event{Kind: EvBlank})
			} else {
				cur.End = pos
				if !put(cur) {
					return
				}
				evs = append(evs, Event{Kind: EvRecord})
			}
			cur = cell.Cell{Start: pos + 1, End: pos + 1}
			lineStart = true
			s = StateN
		}
	}
	return evs, s, cur, lineStart, nil
}

// L 是流式解析器：Feed 任意多次，Close 收尾。单实例非并发安全。
type L struct {
	on   func(Event) error
	lim  Limits
	s    State
	cur  cell.Cell
	ls   bool
	fb   int
	rec  int
	pos  int
	dead *Error
}

// New 创建流式词法器，事件回调 on；回调返回错误则立即进入终态。
func New(on func(Event) error, lim Limits) *L {
	return &L{on: on, lim: lim, ls: true, cur: cell.Cell{}}
}

// BytesSeen 返回状态机处理过的字节总数。
func (l *L) BytesSeen() int { return l.pos }

// Feed 送入一段字节；终态后重复返回同一错误。
func (l *L) Feed(p []byte) error {
	if l.dead != nil {
		return l.dead
	}
	evs, s, cur, ls, e := Run(l.s, l.cur, l.ls, p, l.pos, l.fb, l.lim)
	l.pos += len(p)
	for _, ev := range evs {
		switch ev.Kind {
		case EvField:
			l.fb++
		case EvRecord:
			l.fb = 0
			l.rec++
		case EvBlank:
			l.fb = 0
		}
		if cerr := l.on(ev); cerr != nil {
			if le, ok := cerr.(*Error); ok {
				l.dead = le
			} else {
				l.dead = &Error{Offset: l.pos, Record: l.rec + 1, Field: l.fb}
			}
			l.s, l.cur, l.ls = s, cur, ls
			return l.dead
		}
	}
	l.s, l.cur, l.ls = s, cur, ls
	if e != nil {
		e.Record = l.rec + 1
		if e.Kind == ErrTooManyFields {
			e.Field = l.fb
		}
		l.dead = e
	}
	return e
}

// Close 结束流并判定悬挂状态。
func (l *L) Close() error {
	if l.dead != nil {
		return l.dead
	}
	var e *Error
	switch l.s {
	case StateQ:
		e = &Error{Kind: ErrUnclosedQuote, Offset: l.cur.Start, Record: l.rec + 1, Field: l.fb + 1}
	case StateR:
		e = &Error{Kind: ErrBareCR, Offset: l.pos - 1, Record: l.rec + 1, Field: l.fb + 1}
	default:
		if !l.ls {
			l.cur.End = l.pos
			if l.lim.MaxFields > 0 && l.fb+1 > l.lim.MaxFields {
				e = &Error{Kind: ErrTooManyFields, Offset: l.pos, Record: l.rec + 1, Field: l.fb + 1}
				break
			}
			l.fb++
			if cerr := l.on(Event{Kind: EvField, Cell: l.cur}); cerr != nil {
				e = wrapCB(cerr, l.pos, l.rec+1, l.fb)
				break
			}
			l.fb = 0
			l.rec++
			if cerr := l.on(Event{Kind: EvRecord}); cerr != nil {
				e = wrapCB(cerr, l.pos, l.rec, 1)
				break
			}
		}
	}
	l.ls = true
	if e != nil {
		l.dead = e
	}
	return e
}

func wrapCB(cerr error, pos, rec, field int) *Error {
	if le, ok := cerr.(*Error); ok {
		return le
	}
	return &Error{Offset: pos, Record: rec, Field: field}
}
