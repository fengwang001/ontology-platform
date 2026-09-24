// Package eol 识别流式行尾：\r\n、单独 \r、\n，以及切分点处待定的 \r。
package eol

// Decider 是逐字节行尾判定器。零值即可使用。
//
// 用法：对每个输入字节调用 Feed。返回值含义：
//   - 0：该字节不是行尾，应作为普通内容处理；
//   - 1：该字节是一个行尾（单独 \n，或待定 \r 在 EOF 处被确认）；
//   - 2：\r\n 对，\r 被吸收，仅当前 \n 构成行尾。
//
// Pending 表示上一个字节是尚未看到后继的 \r；EOF 前调用者必须据 Pending
// 把待定 \r 判为一个行尾（norm 在 Close 时处理）。
type Decider struct {
	pendingCR bool
}

// Feed 输入一个字节，返回该字节触发的行尾判定（0/1/2，见类型文档）。
func (d *Decider) Feed(b byte) int {
	if d.pendingCR {
		d.pendingCR = false
		if b == '\n' {
			return 2
		}
		if b == '\r' {
			d.pendingCR = true
		}
		return 1
	}
	if b == '\r' {
		d.pendingCR = true
		return 0
	}
	if b == '\n' {
		return 1
	}
	return 0
}

// Pending 报告是否有一个尚未见到后继字节的 \r。
func (d *Decider) Pending() bool { return d.pendingCR }

// Clear 清掉待定 \r（norm 已在行尾处消费它时调用）。
func (d *Decider) Clear() { d.pendingCR = false }
