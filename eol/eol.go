// Package eol 识别流式输入中的行尾：CRLF、单独 CR、LF。
// 当切分点正好落在 CR 之后时，CR 处于待定状态，直到看到下一个字节。
package eol

// Kind 是 Feed/Flush 返回的事件种类。
type Kind uint8

const (
	None   Kind = iota // 未识别出行尾
	LF                 // 单独 \n
	CR                 // 单独 \r
	CRLF               // \r\n
)

// Decoder 是有状态的行尾识别器，单实例非并发安全。
type Decoder struct {
	pendingCR bool
}

// Feed 输入一个字节 b。first 为待定 \r 先被解析出的事件（可能为 None），
// second 为 b 自身产生的事件（None/CR/LF/CRLF）。
// 若 b=='\r' 且此前已有待定 \r，第一个事件为 CR，新的 \r 继续待定。
func (d *Decoder) Feed(b byte) (first, second Kind) {
	if d.pendingCR {
		if b == '\n' {
			d.pendingCR = false
			return CRLF, None
		}
		first = CR
		d.pendingCR = false
	}
	switch b {
	case '\r':
		d.pendingCR = true
		return first, None
	case '\n':
		return first, LF
	default:
		return first, None
	}
}

// Flush 在流结束时调用：待定的 \r 按单独 CR 处理。
func (d *Decoder) Flush() Kind {
	if d.pendingCR {
		d.pendingCR = false
		return CR
	}
	return None
}

// Pending 报告当前是否有一个等待下一字节判定的 \r。
func (d *Decoder) Pending() bool { return d.pendingCR }
