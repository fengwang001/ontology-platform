// Package lexer 是可暂停续传的 CSV 逐字节状态机，并提供供并行切分复用的纯段运算。
package lexer

import "ontology/cell"

type state uint8

const (
	stField state = iota // 字段开始，尚无内容
	stUnq                // 未引号字段中
	stQuo                // 引号字段中
	stQuoQ               // 引号字段中刚见闭合引号
	stCR                 // 行尾 CR 待定
)

// 段末状态的导出视图，供 par 推导下一段入口模式。
const (
	ExitField = iota
	ExitUnq
	ExitQuo
	ExitQuoQ
	ExitCR
)

// StateCode 返回导出的状态编号。
func (x Exit) StateCode() int { return int(x.State) }

// Mode 是段的入口假设。
type Mode uint8

const (
	ModeOut   Mode = iota // 起点在引号外
	ModeIn                // 起点在引号字段内
	ModeOutCR             // 上一段以引号外的 \r 结束
	ModeOutQ              // 上一段以 sQuoQ 结束
)

const (
	EvCell = iota
	EvEndRec
	EvOpen // 段末悬挂的不完整字段
	EvErr
)

// Event 是段运算产生的事件，偏移均为绝对字节偏移。
type Event struct {
	Kind uint8
	Cell cell.Cell
	Off  int
	Err  *cell.PosError
}

// Exit 描述段结束时的状态，供确定下一段的入口模式。
type Exit struct {
	State state
	Nf    int  // 当前记录已开始的字段数；0 表示不在记录中
	Quo   bool // 最后一个字段是否加引号
	Close int  // 引号字段闭合引号后一位的偏移（sCR 时用）
}

// Sink 接收流式事件。
type Sink interface {
	Cell(c cell.Cell)
	EndRec()
	Error(e *cell.PosError)
}

// Segment 是纯段运算结果。
type Segment struct {
	Events []Event
	Exit   Exit
	Steps  int64
}

// RunSegment 在入口模式 m 下解析 buf[start:end]；last 时执行 EOF 收尾。
func RunSegment(buf []byte, start, end int, m Mode, lim cell.Limits, last bool) Segment {
	e := engine{lim: lim, st: stField}
	e.fstart = start
	e.nf = 1
	switch m {
	case ModeIn:
		e.st, e.nf, e.quoted = stQuo, 1, true
	case ModeOutCR:
		e.st, e.nf = stCR, 1
	case ModeOutQ:
		e.st, e.nf, e.quoted = stQuoQ, 1, true
	}
	for e.pos = start; e.pos < end; e.pos++ {
		e.n++
		e.step(buf[e.pos])
		if e.err != nil {
			break
		}
	}
	if e.err == nil && last {
		e.eof()
	}
	if e.err == nil && e.nf > 0 {
		e.emit(Event{Kind: EvOpen, Cell: cell.Cell{
			Value: string(e.val), Quoted: e.quoted, Start: e.fstart,
			End: -1,
		}})
	}
	return Segment{Events: e.ev, Exit: Exit{
		State: e.st, Nf: e.nf, Quo: e.quoted, Close: e.closeOff,
	}, Steps: e.n}
}

type engine struct {
	sink     Sink
	lim      cell.Limits
	st       state
	nf       int
	quoted   bool
	fstart   int
	closeOff int
	qend     int
	val      []byte
	pos      int
	n        int64
	ev       []Event
	err      *cell.PosError
}

func (e *engine) emit(ev Event) {
	if e.sink != nil {
		switch ev.Kind {
		case EvCell:
			e.sink.Cell(ev.Cell)
		case EvEndRec:
			e.sink.EndRec()
		case EvErr:
			e.sink.Error(ev.Err)
		}
		return
	}
	e.ev = append(e.ev, ev)
}

func (e *engine) fail(err error) {
	e.err = &cell.PosError{Err: err, Offset: e.pos}
	e.emit(Event{Kind: EvErr, Off: e.pos, Err: e.err})
}

func (e *engine) beginField() {
	e.nf++
	e.fstart = e.pos + 1
}

func (e *engine) add(b byte) bool {
	if e.lim.MaxFieldBytes > 0 && len(e.val) >= e.lim.MaxFieldBytes {
		e.fail(cell.ErrFieldTooLong)
		return false
	}
	e.val = append(e.val, b)
	return true
}

func (e *engine) cell(end int) cell.Cell {
	c := cell.Cell{Value: string(e.val), Quoted: e.quoted, Start: e.fstart, End: end}
	e.val = e.val[:0]
	return c
}

func (e *engine) emitCell(end int) {
	e.emit(Event{Kind: EvCell, Cell: e.cell(end)})
}

func (e *engine) endRec() {
	e.emit(Event{Kind: EvEndRec, Off: e.pos})
	e.nf, e.quoted = 1, false
	e.fstart = e.pos + 1
}

func (e *engine) comma() bool {
	if e.lim.MaxFields > 0 && e.nf >= e.lim.MaxFields {
		e.fail(cell.ErrTooManyFields)
		return false
	}
	return true
}

func (e *engine) step(b byte) {
	switch e.st {
	case stField:
		switch {
		case b == ',':
			if e.comma() {
				e.emitCell(e.pos)
				e.beginField()
			}
		case b == '"':
			e.quoted = true
			e.st = stQuo
		case b == '\n':
			e.blankOrCell(e.pos)
			e.endRec()
		case b == '\r':
			e.st = stCR
		default:
			if e.add(b) {
				e.st = stUnq
			}
		}
	case stUnq:
		switch {
		case b == ',':
			if e.comma() {
				e.emitCell(e.pos)
				e.beginField()
				e.st = stField
			}
		case b == '"':
			e.fail(cell.ErrQuoteInBare)
		case b == '\n':
			e.emitCell(e.pos)
			e.endRec()
			e.st = stField
		case b == '\r':
			e.st = stCR
		default:
			e.add(b)
		}
	case stQuo:
		if b == '"' {
			e.qend = e.pos
			e.st = stQuoQ
		} else {
			e.add(b)
		}
	case stQuoQ:
		switch {
		case b == '"':
			if e.add('"') {
				e.st = stQuo
			}
		case b == ',':
			if e.comma() {
				e.emitCell(e.qend + 1)
				e.beginField()
				e.st = stField
			}
		case b == '\n':
			e.emitCell(e.qend + 1)
			e.endRec()
			e.st = stField
		case b == '\r':
			e.closeOff = e.qend + 1
			e.st = stCR
		default:
			e.fail(cell.ErrGarbageAfterQuote)
		}
	case stCR:
		if b != '\n' {
			e.fail(cell.ErrLoneCR)
			return
		}
		if e.nf == 1 && !e.quoted && len(e.val) == 0 {
			// 空行（CRLF）：不产生 Cell
		} else if e.quoted {
			e.emitCell(e.closeOff)
		} else {
			e.emitCell(e.pos - 1)
		}
		e.endRec()
		e.st = stField
	}
}

// blankOrCell：记录首个字段零字节且未加引号 => 空行（不产生 Cell）。
func (e *engine) blankOrCell(end int) {
	if e.nf == 1 && !e.quoted && len(e.val) == 0 {
		return
	}
	e.emitCell(end)
}

func (e *engine) eof() {
	switch e.st {
	case stQuo:
		e.fail(cell.ErrUnclosedQuote)
	case stCR:
		e.fail(cell.ErrLoneCR)
	default:
		if e.nf > 0 {
			end := e.pos
			if e.quoted {
				end = e.qend + 1
			}
			e.emitCell(end)
			e.endRec()
		}
	}
}

// Lexer 是面向流的可续传状态机。单实例非并发安全。
type Lexer struct {
	engine
	term bool
}

// New 创建流式 lexer。
func New(sink Sink, lim cell.Limits) *Lexer {
	return &Lexer{engine: engine{sink: sink, lim: lim, st: stField}}
}

// Feed 追加输入，可任意多次调用。
func (l *Lexer) Feed(p []byte) error {
	if l.term {
		return cell.ErrTerminal
	}
	for _, b := range p {
		l.n++
		l.step(b)
		if l.err != nil {
			l.term = true
			return l.err
		}
	}
	return nil
}

// Close 结束输入。
func (l *Lexer) Close() error {
	if l.term {
		return cell.ErrTerminal
	}
	l.eof()
	if l.err != nil {
		l.term = true
		return l.err
	}
	return nil
}

// Steps 返回字节被处理的总次数。
func (l *Lexer) Steps() int64 { return l.n }
