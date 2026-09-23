// Package eol 识别流式字节中的行尾：\r\n、孤立 \r、\n。
//
// 调用方逐字节 Feed；状态机只回答“本字节与上一个待定 \r 构成什么”。
// 当看到 \r 时它处于待定状态，必须等下一个字节或流结束才能定案。
package eol

// Event 是字节定案结果。
type Event int

const (
	// Other：不是行尾组成部分（含 \n 已在 LFCR/LF 中表达的情形不会出现）。
	Other Event = iota
	// LF：\n 单独成行尾（或 \r\n 中的 \n）。
	LF
	// CR：孤立 \r，自身就是一个行尾。
	CR
	// CRLF：待定 \r 与本字节 \n 合成一个行尾；旧 \r 删除，\n 输出。
	CRLF
	// PendingCR：当前 \r 待定，后续可能与 \n 合成 CRLF。
	PendingCR
)

// Detector 是逐字节行尾状态机。零值即可用，单实例非并发安全。
type Detector struct {
	pending bool
	crPos   int
}

// Feed 送入字节 b（其原文偏移为 pos）。
// settle==CR 表示此前待定 \r 被定案为孤立行尾（调用方先为它输出一个 \n）。
// ev==CRLF 表示待定 \r 与本 \n 配对（删除旧 \r，本 \n 输出一个行尾）。
func (d *Detector) Feed(b byte, pos int) (settle, ev Event, crPos int) {
	if d.pending {
		if b == '\n' {
			d.pending = false
			return Other, CRLF, d.crPos
		}
		d.pending = false
		settle, crPos = CR, d.crPos
	}
	if b == '\r' {
		d.pending, d.crPos = true, pos
		return settle, PendingCR, crPos
	}
	if b == '\n' {
		return settle, LF, crPos
	}
	return settle, Other, crPos
}

// Pending 报告是否有未定案的 \r 及其原文偏移。
func (d *Detector) Pending() (pos int, ok bool) {
	return d.crPos, d.pending
}

// Close 在流结束时定案：有待定 \r 则返回 (crPos, CR)。
func (d *Detector) Close() (pos int, ev Event) {
	if d.pending {
		d.pending = false
		return d.crPos, CR
	}
	return 0, Other
}

// Reset 回到零值状态。
func (d *Detector) Reset() { *d = Detector{} }
