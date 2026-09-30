// Package eol 识别跨切分点的行尾：\r\n、单独 \r、\n。
package eol

// Event 是喂入一个字节后的判定结果。
type Event uint8

const (
	Other     Event = iota // 普通字节
	LF                     // 以 \n 结束的行尾（可能是 \r\n 或单独 \n）
	CR                     // 以单独 \r 结束的行尾
	PendingCR              // 刚看到 \r，是否与后续 \n 配对尚待定
)

// Tracker 是单实例、非并发安全的行尾状态机。
type Tracker struct {
	pending bool
}

// New 返回干净的 Tracker。
func New() *Tracker { return &Tracker{} }

// Pending 报告当前是否有一个尚未判定的 \r。
func (t *Tracker) Pending() bool { return t.pending }

// Feed 喂入下一个原文字节，返回对"上一个待定 \r 或当前字节"的判定。
// 返回 PendingCR 仅表示当前这个 \r 待定，调用方不得立即输出换行。
func (t *Tracker) Feed(b byte) Event {
	if t.pending {
		t.pending = false
		if b == '\n' {
			return LF
		}
		if b == '\r' {
			t.pending = true
			return CR
		}
		return CR
	}
	switch b {
	case '\r':
		t.pending = true
		return PendingCR
	case '\n':
		return LF
	default:
		return Other
	}
}

// Flush 在流结束时结算：待定 \r 视为单独行尾。
func (t *Tracker) Flush() Event {
	if t.pending {
		t.pending = false
		return CR
	}
	return Other
}
