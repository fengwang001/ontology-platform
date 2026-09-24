// Package eol 识别跨行尾切分点的行尾：\r\n、单独 \r、\n，
// 以及 \r 落在切分点处、是否与下一字节组成 \r\n 的待定状态。
package eol

// Decoder 是流式行尾识别器。零值即可使用，不依赖其他包。
// 单实例不要求并发安全。
type Decoder struct {
	pendingCR bool // 上一字节是 \r，等待看当前字节是否为 \n
}

// Kind 是当前字节在「与上一待定 \r 合并观察」后的事件类型。
type Kind uint8

const (
	// Content：普通字节。
	Content Kind = iota
	// LF：单独 \n（上一字节不是 \r）。
	LF
	// CRLF：\r\n 成对出现，事件落在 \n 上，\r 已被消费。
	CRLF
	// LoneCR：单独 \r（下一字符不是 \n），事件落在 \r 上。
	LoneCR
	// PendingCR：当前字节是 \r，是否成对要等下一字节。
	PendingCR
)

// Reset 清空状态，等价于零值。
func (d *Decoder) Reset() { d.pendingCR = false }

// Pending 报告当前是否停在一个待定 \r 上（切分点处使用）。
func (d *Decoder) Pending() bool { return d.pendingCR }

// Feed 送入一个字节，按 [prev,cur] 返回 1~2 个事件，保证不丢字节。
// prev 报告上一个待定 \r 的落定结果（无待定时为 Content）；
// cur 报告当前字节：\n 合成 CRLF；\r 成为新的 PendingCR；其余按类型。
func (d *Decoder) Feed(b byte) (prev, cur Kind) {
	if d.pendingCR {
		d.pendingCR = false
		if b == '\n' {
			return Content, CRLF
		}
		if b == '\r' {
			d.pendingCR = true
			return LoneCR, PendingCR
		}
		if b == '\n' {
			return LoneCR, LF
		}
		return LoneCR, Content
	}
	if b == '\r' {
		d.pendingCR = true
		return Content, PendingCR
	}
	if b == '\n' {
		return Content, LF
	}
	return Content, Content
}

// Flush 在流结束时调用：若还停在待定 \r，它落定为单独行尾并返回 true。
func (d *Decoder) Flush() bool {
	if d.pendingCR {
		d.pendingCR = false
		return true
	}
	return false
}
