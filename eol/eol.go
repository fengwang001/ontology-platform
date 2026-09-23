// Package eol 识别三种行尾（\r\n、单独 \r、\n）以及切分点处待定的 \r。
package eol

// Kind 表示当前字节相对行尾的判定。
type Kind int

const (
	None    Kind = iota // 非行尾字节
	LF                  // 单独 \n，或单独 \r 行尾的提交
	CRLF                // \r\n 中的 \n（\r 应销账，\n 保留）
	Pending             // \r，是否与后续 \n 配对尚待定
)

// Tracker 是跨 Write 的单字节待定状态机：同一时刻至多挂起一个 \r。
type Tracker struct{ pending bool }

// Feed 喂入一个字节，返回 (先前挂起 \r 的判定, 当前字节判定)。
// prev 为 LF 表示挂起 \r 现确认为单独行尾；None 表示无挂起被释放。
// 当 \r 与 \n 配对时返回 (None, CRLF)。
func (t *Tracker) Feed(b byte) (prev, cur Kind) {
	if t.pending {
		t.pending = false
		switch {
		case b == '\n':
			return None, CRLF
		case b == '\r':
			t.pending = true
			return LF, Pending
		default:
			prev = LF
		}
	}
	switch b {
	case '\r':
		t.pending = true
		return prev, Pending
	case '\n':
		return prev, LF
	default:
		return prev, None
	}
}

// Flush 在流结束时调用：挂起的 \r 是单独行尾。
func (t *Tracker) Flush() Kind {
	if t.pending {
		t.pending = false
		return LF
	}
	return None
}

// Pending 报告是否有挂起的 \r。
func (t *Tracker) Pending() bool { return t.pending }
