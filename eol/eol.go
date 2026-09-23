// Package eol 识别流式行尾：\r\n、单独 \r、\n，以及切分点处待定的 \r。
// 本包不依赖其他包。
package eol

// Kind 是当前字节相对行尾的分类。
type Kind int

const (
	Other Kind = iota // 普通字节
	LF                // 独立 \n（不与前一个 \r 配对）
	CRLF2             // \r\n 中处于第二位的 \n
)

// Decoder 是逐字节的行尾状态机，不产生输出，只回答分类，
// 并在切分点（Write 边界）暴露待定 \r 状态。
type Decoder struct{ pendingCR bool }

// Feed 接收下一个字节 b：priorCR 为 true 表示处理 b 之前，一个待定 \r
// 被确认为单独行尾（b 不与它配对）；k 是 b 自身的分类。
// priorCR 为 true 时 b 仍需被正常处理（不会被吞掉）。
func (d *Decoder) Feed(b byte) (priorCR bool, k Kind) {
	prev := d.pendingCR
	d.pendingCR = false
	switch {
	case b == '\n':
		if prev {
			return false, CRLF2
		}
		return false, LF
	case b == '\r':
		d.pendingCR = true
		return prev, Other
	default:
		return prev, Other
	}
}

// PendingCR 报告当前是否有 \r 悬而未决（可能与下一段的 \n 配对）。
func (d *Decoder) PendingCR() bool { return d.pendingCR }

// Flush 表示流结束：清空待定状态（由调用方决定如何输出该 \r）。
func (d *Decoder) Flush() { d.pendingCR = false }
