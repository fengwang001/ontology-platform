// Package lexer 是可暂停/续传的逐字节 CSV 状态机。每输入字节恰好处理一次。
package lexer

import (
	"strings"

	"ontology/cell"
)

// Kind 标记事件种类。
type Kind int

const (
	Field  Kind = iota // 一个完整字段
	Record             // 一条记录结束（最后一个 Field 已先行发出）
)

// Event 是词法事件；偏移均相对于本段起点。
type Event struct {
	Kind   Kind
	Cell   cell.Cell
	Offset int
}

// Start 是段解析的起始假设。
type Start int

const (
	StartOutside Start = iota // 段首在引号外、行首
	StartInside               // 段首在引号内（携带起始于段首的悬挂字段）
)

type state int

const (
	sFieldStart state = iota
	sBare
	sQuoted
	sQQuote
	sCR      // 字段末尾的 CR 待定
	sCRBlank // 空行的 CR 待定
)

// Lexer 持有跨 Feed 的全部解析状态。单实例非并发安全。
type Lexer struct {
	maxField int
	st       state
	off      int
	fieldNo  int
	val      strings.Builder
	fieldBeg int
	bare     bool
	pending  bool
	fresh    bool // 处于行首且尚未产生字段
	bytes    int64
	err      *cell.Error
}

// New 创建 Lexer；maxField<=0 不限字段大小；start 为段起始假设。
func New(maxField int, start Start) *Lexer {
	l := &Lexer{maxField: maxField, st: sFieldStart, fresh: true}
	if start == StartInside {
		l.st, l.pending, l.bare, l.fresh, l.fieldBeg = sQuoted, true, false, false, 0
	}
	return l
}

// Bytes 返回状态机处理过的字节总数。
func (l *Lexer) Bytes() int64 { return l.bytes }

// Inside 报告当前是否停在引号字段内部（par 拼接下一段假设用）。
func (l *Lexer) Inside() bool { return l.st == sQuoted }

// Err 返回终态错误（无则 nil）。
func (l *Lexer) Err() *cell.Error { return l.err }

func (l *Lexer) fail(err error, off int) *cell.Error {
	if l.err == nil {
		l.err = cell.NewError(err, off, 0, 0)
	}
	return l.err
}

func (l *Lexer) addByte(b byte, off int) bool {
	if l.maxField > 0 && l.val.Len() >= l.maxField {
		l.fail(cell.ErrFieldTooLarge, off)
		return false
	}
	l.val.WriteByte(b)
	return true
}

func (l *Lexer) emit(off int) Event {
	l.fieldNo++
	c := cell.Cell{Value: l.val.String(), Quoted: !l.bare, Start: l.fieldBeg, End: off}
	l.val.Reset()
	l.pending, l.fresh = false, false
	return Event{Kind: Field, Cell: c, Offset: off}
}

// Feed 送入一段字节，返回新产生的事件。
func (l *Lexer) Feed(p []byte) ([]Event, *cell.Error) {
	var evs []Event
	for _, b := range p {
		l.bytes++
		off := l.off
		l.off++
		if l.err != nil {
			return evs, l.err
		}
		switch l.st {
		case sFieldStart:
			l.fieldBeg, l.pending, l.bare = off, true, true
			switch b {
			case ',':
				l.fresh = false
				evs = append(evs, l.emit(off))
			case '\n':
				l.pending, l.fresh = false, true // 空行跳过
			case '\r':
				if l.fresh {
					l.st = sCRBlank
				} else {
					l.st = sCR
				}
			case '"':
				l.bare, l.st, l.fresh = false, sQuoted, false
			default:
				l.st, l.fresh = sBare, false
				if !l.addByte(b, off) {
					return evs, l.err
				}
			}
		case sBare:
			switch b {
			case ',':
				evs = append(evs, l.emit(off))
			case '\n':
				evs = append(evs, l.emit(off), Event{Kind: Record, Offset: off})
				l.st, l.fresh = sFieldStart, true
			case '\r':
				l.st = sCR
			case '"':
				return evs, l.fail(cell.ErrBareQuote, off)
			default:
				if !l.addByte(b, off) {
					return evs, l.err
				}
			}
		case sQuoted:
			switch b {
			case '"':
				l.st = sQQuote
			default:
				if !l.addByte(b, off) {
					return evs, l.err
				}
			}
		case sQQuote:
			switch b {
			case '"':
				l.st = sQuoted
				if !l.addByte('"', off) {
					return evs, l.err
				}
			case ',':
				evs = append(evs, l.emit(off))
			case '\n':
				evs = append(evs, l.emit(off), Event{Kind: Record, Offset: off})
				l.st, l.fresh = sFieldStart, true
			case '\r':
				l.st = sCR
			default:
				return evs, l.fail(cell.ErrCharsAfterQuote, off)
			}
		case sCR:
			if b != '\n' {
				return evs, l.fail(cell.ErrLoneCR, off-1)
			}
			evs = append(evs, l.emit(off-1), Event{Kind: Record, Offset: off})
			l.st, l.fresh = sFieldStart, true
		case sCRBlank:
			if b != '\n' {
				return evs, l.fail(cell.ErrLoneCR, off-1)
			}
			l.st, l.pending, l.fresh = sFieldStart, false, true
		}
	}
	return evs, nil
}

// Close 结束流：挂起的合法字段作为末记录发出；非法状态报错。
func (l *Lexer) Close() ([]Event, *cell.Error) {
	var evs []Event
	if l.err != nil {
		return evs, l.err
	}
	switch l.st {
	case sQuoted:
		return evs, l.fail(cell.ErrUnclosedQuote, l.off)
	case sCR, sCRBlank:
		return evs, l.fail(cell.ErrLoneCR, l.off-1)
	case sBare, sQQuote:
		if l.pending {
			evs = append(evs, l.emit(l.off), Event{Kind: Record, Offset: l.off})
		}
	}
	return evs, nil
}
