// Package lexer 是可暂停续传的 CSV 逐字节状态机，事件驱动 Sink。
package lexer

import "ontology/cell"

// Kind: 字段开始 / 值片段 / 字段结束 / 记录结束(Dirty=false 即空行，跳过)。
const (
	EvFieldStart = iota
	EvValue
	EvFieldEnd
	EvRecordEnd
)

// Event 偏移为相对本段输入的 0 起字节偏移。
type Event struct {
	Kind          int
	Offset, End   int
	Data          []byte
	Quoted, Dirty bool
}

// Sink 接收事件；返回非 nil 错误会让状态机立刻进入终态。
type Sink interface {
	Event(ev Event) error
	Error(kind error, offset int) error
}

const (
	stStart = iota
	stBare
	stQuoted
	stQuoteSeen
	stCR
)

// Entry 描述段入口状态；Exit 描述段末状态。
type Entry struct{ InQuoted, AfterQuote, Dirty bool }
type Exit struct {
	State  int
	Dirty  bool
	FStart int
	Quoted bool
}

// M 是可重入的状态机核心；Bytes 记录本段被处理字节数。
type M struct {
	st                       int
	recDirty, fDirty, quoted bool
	fstart                   int
	sink                     Sink
	Bytes                    int
}

// Run 用入口状态 e 解析 p（相对偏移从 0 起）。
func (m *M) Run(p []byte, e Entry, sink Sink) (Exit, error) {
	m.st, m.recDirty, m.fDirty, m.quoted, m.sink, m.Bytes = stStart, false, false, false, sink, 0
	switch {
	case e.InQuoted:
		m.st, m.quoted, m.recDirty, m.fDirty, m.fstart = stQuoted, true, true, true, -1
	case e.Dirty:
		m.st, m.recDirty, m.fDirty = stBare, true, true
	case len(p) > 0:
		m.fstart = 0
		if err := m.ev(EvFieldStart, 0, 0, nil, false, false); err != nil {
			return Exit{}, err
		}
	}
	for i := 0; i < len(p); i++ {
		if err := m.step(p[i], i, p); err != nil {
			return Exit{}, err
		}
	}
	return Exit{m.st, m.recDirty, m.fstart, m.quoted}, nil
}

func (m *M) ev(k, off, end int, d []byte, q, dirty bool) error {
	return m.sink.Event(Event{k, off, end, d, q, dirty})
}
func (m *M) fail(k error, at int) error { return m.sink.Error(k, at) }

func (m *M) step(c byte, i int, p []byte) error {
	m.Bytes++
	switch m.st {
	case stStart:
		return m.fromStart(c, i, p)
	case stBare:
		return m.fromBare(c, i, p)
	case stQuoted:
		if c == '"' {
			m.st = stQuoteSeen
			return nil
		}
		m.recDirty, m.fDirty = true, true
		return m.ev(EvValue, i, i+1, p[i:i+1], false, false)
	case stQuoteSeen:
		return m.fromSeen(c, i)
	}
	if c != '\n' {
		return m.fail(cell.ErrLoneCR, i-1)
	}
	return m.endRecord(i + 1)
}

func (m *M) nextField(i int) error {
	m.fDirty, m.quoted, m.fstart = false, false, i
	return m.ev(EvFieldStart, i, 0, nil, false, false)
}

func (m *M) fieldEnd(end int, dirty bool) error {
	return m.ev(EvFieldEnd, m.fstart, end, nil, m.quoted, dirty)
}

func (m *M) endRecord(end int) error {
	if m.recDirty {
		if err := m.fieldEnd(end, m.fDirty); err != nil {
			return err
		}
	}
	dirty := m.recDirty
	m.recDirty, m.fDirty, m.quoted = false, false, false
	if err := m.ev(EvRecordEnd, end, 0, nil, false, dirty); err != nil {
		return err
	}
	return m.nextField(end)
}

func (m *M) fromStart(c byte, i int, p []byte) error {
	switch {
	case c == '"':
		m.st, m.quoted, m.recDirty, m.fDirty = stQuoted, true, true, true
	case c == ',':
		if err := m.fieldEnd(i, false); err != nil {
			return err
		}
		m.st = stStart
		return m.nextField(i + 1)
	case c == '\n':
		return m.endRecord(i + 1)
	case c == '\r':
		m.st = stCR
	default:
		m.st, m.recDirty, m.fDirty = stBare, true, true
		return m.ev(EvValue, i, i+1, p[i:i+1], false, false)
	}
	return nil
}

func (m *M) fromBare(c byte, i int, p []byte) error {
	switch {
	case c == ',':
		if err := m.fieldEnd(i, true); err != nil {
			return err
		}
		m.st = stStart
		return m.nextField(i + 1)
	case c == '"':
		return m.fail(cell.ErrBareQuote, i)
	case c == '\n':
		return m.endRecord(i + 1)
	case c == '\r':
		m.st = stCR
	default:
		return m.ev(EvValue, i, i+1, p[i:i+1], false, false)
	}
	return nil
}

func (m *M) fromSeen(c byte, i int) error {
	switch {
	case c == '"':
		m.st, m.recDirty, m.fDirty = stQuoted, true, true
		return m.ev(EvValue, i-1, i+1, []byte{'"'}, false, false)
	case c == ',':
		if err := m.fieldEnd(i+1, true); err != nil {
			return err
		}
		m.st = stStart
		return m.nextField(i + 2)
	case c == '\n':
		return m.endRecord(i + 2)
	case c == '\r':
		m.st = stCR
	default:
		return m.fail(cell.ErrQuoteAfterClose, i)
	}
	return nil
}

// End 处理段末/流末。
func (m *M) End(n int) error {
	switch m.st {
	case stQuoted:
		at := m.fstart
		if at < 0 {
			at = 0
		}
		return m.fail(cell.ErrUnclosedQuote, at)
	case stCR:
		return m.fail(cell.ErrLoneCR, n-1)
	default:
		if m.recDirty {
			return m.endRecord(n)
		}
	}
	return nil
}
