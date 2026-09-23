package lexer

// 状态机状态。
const (
	stField = iota // 字段开始
	stBare         // 未引号字段中
	stQuote        // 引号字段中
	stAfterQuote   // 引号字段内刚见到一个引号
	stCR           // 行尾 CR 待定
)

// EventKind 是扫描器产出的低层事件类别。
type EventKind uint8

const (
	EvFieldStart EventKind = iota
	EvAppend
	EvFieldEnd
	EvRecordEnd
)

// Event 是扫描事件。Off 为绝对字节偏移；Byte 仅 EvAppend；Quote 仅 FieldStart/End。
type Event struct {
	Kind  EventKind
	Off   int
	Byte  byte
	Quote bool
}

// Entry 描述一个段的入口假设，供半包续传与并行切分复用。
type Entry struct {
	state  int
	active bool // 入口时字段已在进行（不补发 EvFieldStart）
	crPos  int
	closeQ int
}

// 预置入口。
var (
	EntryStart      = Entry{state: stField}
	EntryBareMid    = Entry{state: stBare, active: true}
	EntryQuoteMid   = Entry{state: stQuote, active: true}
	EntryAfterQuote = Entry{state: stAfterQuote, active: true}
)

// EntryCR 是「上一段以未决 \r 结束」时的入口，pos 为该 \r 的全局偏移。
func EntryCR(pos int) Entry { return Entry{state: stCR, crPos: pos} }

// Resume 用选定出口帧与段尾 \r 偏移构造下段入口。
func Resume(prev Frame) Entry {
	return Entry{state: prev.State, active: prev.Active, closeQ: prev.CloseQ}
}

// Frame 是可暂停可续传的逐字节状态机帧。纯数据，可在段间复制。
type Frame struct {
	State   int
	Active  bool
	Begun   bool
	CRPos   int
	CloseQ  int
	baseOff int
	ticks   int64 // 非导出：字节被状态机处理的总次数
}

// Ticks 返回该帧处理过的字节总数（每字节恰一次）。
func (f *Frame) Ticks() int64 { return f.ticks }

// Begin 以入口假设初始化，解析从全局偏移 base 开始。
func (f *Frame) Begin(e Entry, base int) {
	*f = Frame{State: e.state, Active: e.active, CRPos: e.crPos,
		CloseQ: e.closeQ, baseOff: base}
}

// Scan 把 p 中每个字节恰好喂状态机一次，事件追加到 ev。
func (f *Frame) Scan(p []byte, ev *[]Event) *Error {
	for i, b := range p {
		f.ticks++
		off := f.baseOff + i
		if f.State == stCR {
			if b != '\n' {
				return mkErr(ErrLoneCR, f.CRPos)
			}
			f.finishRecord(false, off, ev)
			continue
		}
		if e := f.step(b, off, ev); e != nil {
			return e
		}
	}
	return nil
}

func (f *Frame) step(b byte, off int, ev *[]Event) *Error {
	switch b {
	case ',':
		if f.State == stQuote {
			f.append(b, off, ev)
			return nil
		}
		f.finishField(f.State == stAfterQuote, off, ev)
		f.reset()
	case '"':
		switch f.State {
		case stField:
			f.Active = true
			f.State = stQuote
			*ev = append(*ev, Event{Kind: EvFieldStart, Off: off, Quote: true})
		case stBare:
			return mkErr(ErrBareQuote, off)
		case stQuote:
			f.State, f.CloseQ = stAfterQuote, off
		default:
			f.State = stQuote
			f.append('"', off, ev)
		}
	case '\r':
		switch f.State {
		case stQuote:
			f.append(b, off, ev)
		case stAfterQuote:
			return mkErr(ErrQuoteClose, off)
		default:
			f.State, f.CRPos = stCR, off
		}
	case '\n':
		if f.State == stQuote {
			f.append(b, off, ev)
			return nil
		}
		f.finishRecord(f.State == stAfterQuote, off, ev)
	default:
		switch f.State {
		case stAfterQuote:
			return mkErr(ErrQuoteClose, off)
		case stField:
			f.Active, f.State = true, stBare
			*ev = append(*ev, Event{Kind: EvFieldStart, Off: off})
			fallthrough
		case stBare, stQuote:
			f.append(b, off, ev)
		}
	}
	return nil
}

func (f *Frame) append(b byte, off int, ev *[]Event) {
	*ev = append(*ev, Event{Kind: EvAppend, Off: off, Byte: b})
}

func (f *Frame) finishField(quoted bool, off int, ev *[]Event) {
	if !f.Active {
		f.Active = true
		*ev = append(*ev, Event{Kind: EvFieldStart, Off: off, Quote: false})
	}
	*ev = append(*ev, Event{Kind: EvFieldEnd, Off: off, Quote: quoted})
	f.Begun = true
}

func (f *Frame) finishRecord(quoted bool, off int, ev *[]Event) {
	f.finishField(quoted, off, ev)
	*ev = append(*ev, Event{Kind: EvRecordEnd, Off: off})
	f.reset()
}

func (f *Frame) reset() {
	f.Active, f.CRPos, f.CloseQ = false, 0, 0
	f.State = stField
}

// Finish 处理流结束。
func (f *Frame) Finish(ev *[]Event) *Error {
	switch f.State {
	case stCR:
		return mkErr(ErrLoneCR, f.CRPos)
	case stQuote:
		return mkErr(ErrUnclosedQuote, f.baseOff)
	case stAfterQuote:
		f.finishField(true, f.CloseQ+1, ev)
		*ev = append(*ev, Event{Kind: EvRecordEnd, Off: f.CloseQ})
	case stBare:
		f.finishField(false, f.baseOff, ev)
		*ev = append(*ev, Event{Kind: EvRecordEnd, Off: f.baseOff})
	}
	return nil
}
