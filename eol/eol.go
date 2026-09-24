// Package eol 识别混杂行尾：\r\n、单独 \r、\n，以及切分点处待定的 \r。
package eol

// Event 是喂入一个字节后产生的规范化事件。
type Event uint8

const (
	// Content 普通字节，原样通过。
	Content Event = iota
	// LineStart 待定 \r 被后续内容证实为单独行尾，此刻补一个 \n。
	LineStart
	// PendingCR 该 \r 可能与下一个字节的 \n 配成 \r\n，暂不输出。
	PendingCR
	// PairLF 该 \n 与挂起的 \r 组成 \r\n，输出一个 \n（\r 将被删除）。
	PairLF
	// SoloLF 单独 \n，输出一个 \n。
	SoloLF
)

// Scanner 是单字节驱动的行尾状态机。非并发安全。
type Scanner struct {
	pending bool
}

// Pending 报告是否有一个尚未定性的 \r。
func (s *Scanner) Pending() bool { return s.pending }

// Feed 喂入一个字节，返回零个、一个或两个事件（顺序即输出顺序）。
func (s *Scanner) Feed(b byte) []Event {
	if s.pending {
		s.pending = false
		switch b {
		case '\n':
			return []Event{PairLF}
		case '\r':
			s.pending = true
			return []Event{LineStart, PendingCR}
		default:
			return []Event{LineStart, Content}
		}
	}
	if b == '\r' {
		s.pending = true
		return []Event{PendingCR}
	}
	if b == '\n' {
		return []Event{SoloLF}
	}
	return []Event{Content}
}

// Flush 在流结束（或 chunk 边界）时收尾：待定 \r 按单独行尾处理。
// 返回空事件或一个 LineStart。
func (s *Scanner) Flush() []Event {
	if !s.pending {
		return nil
	}
	s.pending = false
	return []Event{LineStart}
}
