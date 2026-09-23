package lexer

import "sync/atomic"

var segProcessed atomic.Uint64

type state int

const (
	stFS state = iota
	stBare
	stQ
	stQS
	stCR
)

// machine 是与 I/O 形态无关的纯状态机：既可直推 Sink，也可收集为片段。
type machine struct {
	sink      Sink
	maxField  int
	base      int
	seg       bool
	st        state
	buf       []byte
	quoted    bool
	fstart    int // 当前字段起点绝对偏移（fs/bare 用）
	fieldCnt  int // 当前记录已产出字段数
	nbytes    int // 当前字段字节数（"" 计 1）
	processed uint64
	crClosed  bool
	crOff     int
	inside    bool // 段首假设已在引号内
}

func newMachine(sink Sink, maxField, base int, seg, inside bool, insideStart int) *machine {
	m := &machine{sink: sink, maxField: maxField, base: base, seg: seg, inside: inside}
	if inside {
		m.st = stQ
		m.quoted = true
		m.fstart = insideStart // 开引号位于本段之前
	} else {
		m.fstart = base
	}
	return m
}

func (m *machine) endMode() EndMode {
	switch m.st {
	case stBare:
		return EndBare
	case stQ:
		return EndQuote
	case stQS:
		return EndQSeen
	case stCR:
		return EndCR
	default:
		return EndBoundary
	}
}

func (m *machine) fail(kind error, off int) error {
	return &LexError{Kind: kind, Off: off}
}

// addByte 计入一个字段内容字节，超限立刻拒绝（"" 按一字符由调用方控制）。
func (m *machine) addByte(b byte, off int) error {
	m.nbytes++
	if m.maxField > 0 && m.nbytes > m.maxField {
		return m.fail(ErrFieldTooLong, off)
	}
	m.buf = append(m.buf, b)
	return nil
}

func (m *machine) emit(off int) error {
	start := m.fstart
	if m.quoted {
		start = m.fstart
	}
	if err := m.sink.Field(Field{
		Value:  string(m.buf),
		Quoted: m.quoted,
		Start:  start,
		End:    off,
	}); err != nil {
		return err
	}
	m.buf = m.buf[:0]
	m.quoted = false
	m.nbytes = 0
	m.fieldCnt++
	return nil
}

func (m *machine) resetRecord(nextOff int) {
	m.fieldCnt = 0
	m.fstart = nextOff
}

// run 处理 p；eof 时执行流收尾。段机以 eof=false 调用，悬挂字段保留不 emit。
func (m *machine) run(p []byte, eof bool) error {
	for i := 0; i < len(p); i++ {
		off := m.base + i
		b := p[i]
		m.processed++
		switch m.st {
		case stFS:
			switch b {
			case ',':
				if err := m.emit(off); err != nil {
					return err
				}
				m.st = stFS
				m.fstart = off + 1
			case '"':
				m.quoted = true
				m.st = stQ
			case '\r':
				m.crClosed = false
				m.crOff = off
				m.st = stCR
			case '\n':
				if m.fieldCnt == 0 { // 空行：跳过
					m.fstart = off + 1
				} else {
					if err := m.sink.EndRecord(); err != nil {
						return err
					}
					m.resetRecord(off + 1)
				}
			default:
				if err := m.addByte(b, off); err != nil {
					return err
				}
				m.st = stBare
			}
		case stBare:
			switch b {
			case ',':
				if err := m.emit(off); err != nil {
					return err
				}
				m.st = stFS
				m.fstart = off + 1
			case '"':
				return m.fail(ErrBareQuote, off)
			case '\r':
				m.crClosed = false
				m.crOff = off
				m.st = stCR
			case '\n':
				if err := m.emit(off); err != nil {
					return err
				}
				if err := m.sink.EndRecord(); err != nil {
					return err
				}
				m.resetRecord(off + 1)
				m.st = stFS
			default:
				if err := m.addByte(b, off); err != nil {
					return err
				}
			}
		case stQ:
			if b == '"' {
				m.st = stQS
				continue
			}
			if err := m.addByte(b, off); err != nil {
				return err
			}
		case stQS:
			switch b {
			case '"': // 转义的引号：计一个字符
				if err := m.addByte('"', off); err != nil {
					return err
				}
				m.st = stQ
			case ',':
				if err := m.emit(off + 1); err != nil {
					return err
				}
				m.st = stFS
				m.fstart = off + 1
			case '\r':
				m.crClosed = true
				m.crOff = off
				m.st = stCR
			case '\n':
				if err := m.emit(off + 1); err != nil {
					return err
				}
				if err := m.sink.EndRecord(); err != nil {
					return err
				}
				m.resetRecord(off + 1)
				m.st = stFS
			default:
				return m.fail(ErrExtraQuote, off)
			}
		case stCR:
			if b == '\n' {
				if m.crClosed {
					if err := m.emit(off); err != nil { // off 指向 \n，关闭引号在 off-1
						return err
					}
				} else if m.fieldCnt > 0 || len(m.buf) > 0 || m.st == stCR && m.crPendingField() {
					// 裸字段在见到 \r 时尚未 emit
				}
				if !m.crClosed {
					// 裸字段以 \r\n 结束：\r 位于 off-1
					if m.fieldCnt == 0 && len(m.buf) == 0 {
						// 空记录（无任何字段）→ 空行语义下不应发生；按空行跳过
						m.resetRecord(off + 1)
						m.st = stFS
						continue
					}
					if err := m.emit(off - 1); err != nil {
						return err
					}
				}
				if err := m.sink.EndRecord(); err != nil {
					return err
				}
				m.resetRecord(off + 1)
				m.st = stFS
			} else {
				return m.fail(ErrDanglingCR, m.crOff)
			}
		}
	}
	if eof {
		return m.finish()
	}
	if m.seg {
		segProcessed.Add(m.processed)
	}
	return nil
}

func (m *machine) crPendingField() bool { return false }

func (m *machine) finish() error {
	switch m.st {
	case stCR:
		return m.fail(ErrDanglingCR, m.crOff)
	case stQ:
		return m.fail(ErrUnterminated, m.base)
	case stQS:
		if err := m.emit(m.base); err != nil {
			return err
		}
		return m.sink.EndRecord()
	case stBare:
		if err := m.emit(m.base); err != nil {
			return err
		}
		return m.sink.EndRecord()
	case stFS:
		// 字段已由逗号产出但记录未闭合（如结尾 "a,"）：补一个空字段。
		if m.fieldCnt > 0 {
			if err := m.emit(m.base); err != nil {
				return err
			}
			return m.sink.EndRecord()
		}
	}
	return nil
}
