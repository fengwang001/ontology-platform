// Package eol 识别混合行尾：\r\n、单独 \r、\n，以及切分点处待定的 \r。
package eol

// Event 表示 Feed 产出的一个判定事件。
type Event struct {
	Kind Kind // CRPending 表示 \r 暂存，等待下一字节
	Byte byte // CRPending/Other 时携带的原字节
}

// Kind 为事件种类。
type Kind uint8

const (
	// Other 为普通字节（含 NUL、非法 UTF-8 字节，原样通过）。
	Other Kind = iota
	// LF 表示一个确定的行尾（\n、\r\n、孤立 \r 在 Flush 时）规范化为 \n。
	LF
	// CRDeleted 表示 \r\n 中被删除的 \r。
	CRDeleted
	// CRPending 表示切分点处暂存的待定 \r。
	CRPending
)

// Scanner 是有状态的行尾识别器，单实例不要求并发安全。
type Scanner struct {
	pendingCR bool
}

// New 返回空状态识别器。
func New() *Scanner { return &Scanner{} }

// Feed 喂入一个字节，返回 1~2 个事件。
// \r\n：先消化待定 \r（CRDeleted）再给 LF；\r 后非 \n：先 LF（孤立 \r）再 Other。
func (s *Scanner) Feed(b byte) []Event {
	ev := make([]Event, 0, 2)
	if s.pendingCR {
		s.pendingCR = false
		if b == '\n' {
			ev = append(ev, Event{Kind: CRDeleted}, Event{Kind: LF, Byte: '\n'})
			return ev
		}
		ev = append(ev, Event{Kind: LF, Byte: '\n'})
	}
	switch {
	case b == '\n':
		ev = append(ev, Event{Kind: LF, Byte: '\n'})
	case b == '\r':
		s.pendingCR = true
		ev = append(ev, Event{Kind: CRPending, Byte: '\r'})
	default:
		ev = append(ev, Event{Kind: Other, Byte: b})
	}
	return ev
}

// Pending 报告是否存在切分点处待定的 \r。
func (s *Scanner) Pending() bool { return s.pendingCR }

// Flush 返回流结束时待定 \r 的判定（孤立行尾→LF），并清空状态。
func (s *Scanner) Flush() []Event {
	if !s.pendingCR {
		return nil
	}
	s.pendingCR = false
	return []Event{{Kind: LF, Byte: '\n'}}
}

// Reset 清空所有状态。
func (s *Scanner) Reset() { s.pendingCR = false }
