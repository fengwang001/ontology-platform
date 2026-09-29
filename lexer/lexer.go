// Package lexer 是逐字节 CSV 状态机，可暂停续传。单实例非并发安全。
package lexer

import (
	"errors"

	"ontology/cell"
)

// ErrClosed 在 Close 之后继续 Feed/Close 时返回。
var ErrClosed = errors.New("csv: feed after close")

// State 为状态机状态。
type State int

const (
	FieldStart State = iota // 字段开始
	Unquoted                // 未引号字段中
	Quoted                  // 引号字段中
	QuoteSeen               // 引号字段中刚见到一个引号
	CRSeen                  // 行尾 CR 待定
)

// Config 为可配置上限，0 表示不限制。
type Config struct {
	MaxFieldBytes int
	MaxFields     int
}

// Event 是一个字段事件（End=false）或记录结束事件（End=true）。
type Event struct {
	Cell cell.Cell
	End  bool
}

// Lexer 逐字节处理输入，每字节恰好处理一次，不回扫。
type Lexer struct {
	cfg        Config
	state      State
	buf        []byte
	fSize      int
	fQuoted    bool
	fStart     int
	pos        int
	recNo      int
	fldNo      int
	recActive  bool
	closed     bool
	err        error
	ev         []Event
	processed  int64
}

// New 创建从偏移 base 开始、处于 FieldStart 的解析器。
func New(cfg Config, base int) *Lexer {
	return &Lexer{cfg: cfg, state: FieldStart, pos: base, fStart: base, recNo: 1}
}

// NewEntry 创建指定进入状态的解析器（供 par 的引号内假设使用）。
func NewEntry(cfg Config, base int, st State, quoted bool) *Lexer {
	l := New(cfg, base)
	l.state, l.fQuoted, l.recActive = st, quoted, true
	return l
}

// Feed 处理一段字节，可调用任意多次；出错后进入终态并记住错误。
func (l *Lexer) Feed(p []byte) error {
	if l.err != nil {
		return l.err
	}
	if l.closed {
		l.err = ErrClosed
		return l.err
	}
	for _, b := range p {
		l.step(b)
		l.pos++
		if l.err != nil {
			return l.err
		}
	}
	return nil
}

// Close 结束输入，产出末尾无换行的最后一条记录（若有）。
func (l *Lexer) Close() error {
	if l.err != nil {
		return l.err
	}
	if l.closed {
		return ErrClosed
	}
	l.closed = true
	switch l.state {
	case Quoted:
		l.fail(cell.ErrUnterminatedQuote, l.pos)
	case CRSeen:
		l.fail(cell.ErrDanglingCR, l.pos-1)
	case FieldStart:
		if l.recActive {
			l.endRecord()
		}
	default: // Unquoted / QuoteSeen：末尾记录无换行，合法
		l.endRecord()
	}
	return l.err
}

// Events 返回并清空已累积的事件。
func (l *Lexer) Events() []Event { e := l.ev; l.ev = nil; return e }

// Processed 返回被状态机处理过的字节总次数。
func (l *Lexer) Processed() int64 { return l.processed }

// Pos 返回下一个待处理字节的全局偏移。
func (l *Lexer) Pos() int { return l.pos }

// Snapshot 描述当前待定状态，供 par 拼接进位。
type Snapshot struct {
	State   State
	Quoted  bool
	Active  bool
	Fields  int
	Records int
	Content []byte
	Start   int
}

// Snap 导出当前待定状态。
func (l *Lexer) Snap() Snapshot {
	return Snapshot{l.state, l.fQuoted, l.recActive, l.fldNo, l.recNo - 1,
		append([]byte(nil), l.buf...), l.fStart}
}

func (l *Lexer) fail(kind error, off int) {
	l.err = &cell.Error{Kind: kind, Offset: off, Record: l.recNo, Field: l.fldNo + 1}
}

func (l *Lexer) addContent(b byte) {
	l.fSize++
	if l.cfg.MaxFieldBytes > 0 && l.fSize > l.cfg.MaxFieldBytes {
		l.fail(cell.ErrFieldTooLarge, l.pos)
		return
	}
	l.buf = append(l.buf, b)
}

func (l *Lexer) emitField() {
	if l.cfg.MaxFields > 0 && l.fldNo+1 > l.cfg.MaxFields {
		l.fail(cell.ErrTooManyFields, l.pos)
		return
	}
	end := l.pos
	if l.state == CRSeen {
		end = l.pos - 1 // 排除行尾 \r
	}
	l.ev = append(l.ev, Event{Cell: cell.Cell{
		Value: string(l.buf), Quoted: l.fQuoted, Start: l.fStart, End: end}})
	l.buf, l.fSize, l.fQuoted = l.buf[:0], 0, false
	l.fStart = l.pos + 1
	l.fldNo++
}

func (l *Lexer) endRecord() {
	l.emitField()
	if l.err != nil {
		return
	}
	l.ev = append(l.ev, Event{End: true})
	l.recNo++
	l.fldNo = 0
	l.recActive = false
	l.state = FieldStart
}

func (l *Lexer) step(b byte) {
	l.processed++
	switch l.state {
	case FieldStart:
		switch b {
		case ',':
			l.emitField()
		case '"':
			l.state, l.fQuoted, l.recActive = Quoted, true, true
		case '\n':
			l.endRecord()
		case '\r':
			l.state, l.recActive = CRSeen, true
		default:
			l.state, l.recActive = Unquoted, true
			l.addContent(b)
		}
	case Unquoted:
		switch b {
		case ',':
			l.emitField()
			l.state = FieldStart
		case '"':
			l.fail(cell.ErrBareQuote, l.pos)
		case '\n':
			l.endRecord()
		case '\r':
			l.state = CRSeen
		default:
			l.addContent(b)
		}
	case Quoted:
		if b == '"' {
			l.state = QuoteSeen
		} else {
			l.addContent(b)
		}
	case QuoteSeen:
		switch b {
		case ',':
			l.emitField()
			l.state = FieldStart
		case '"':
			l.state = Quoted
			l.addContent(b)
		case '\n':
			l.endRecord()
		case '\r':
			l.state = CRSeen
		default:
			l.fail(cell.ErrQuoteFollow, l.pos)
		}
	case CRSeen:
		if b == '\n' {
			l.endRecord()
		} else {
			l.fail(cell.ErrDanglingCR, l.pos-1)
		}
	}
}
