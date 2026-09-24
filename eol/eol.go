// Package eol 识别流中的行尾：\r\n、单独 \r、\n，以及切分点处待定的 \r。
package eol

import "errors"

// Event 是喂入一个字节后的判定结果。
type Event uint8

const (
	// Other 当前字节不是行尾组成部分。
	Other Event = iota
	// LFLine 当前字节构成一个以 \n 结尾的行尾（单独 \n 或 \r\n）。
	LFLine
	// CRLine 前一个待定 \r 在当前字节处被确认为独立行尾；当前字节另行报告。
	CRLine
	// PendingCR 当前字节是待定 \r：它可能与下一个 \n 配对。
	PendingCR
)

// ErrFlushed 表示 Flush 后解码器已终结，不能再喂字节。
var ErrFlushed = errors.New("eol: decoder flushed")

// Decoder 是逐字节的行尾状态机，可跨任意 Write 切分保持状态。
type Decoder struct {
	pendingCR bool
	flushed   bool
}

// Reset 使解码器回到初始状态。
func (d *Decoder) Reset() { d.pendingCR, d.flushed = false, false }

// PendingCR 报告末尾是否悬着一个尚未确认的 \r。
func (d *Decoder) PendingCR() bool { return d.pendingCR }

// Step 喂入字节 b，返回该字节对应的事件。
// 唯一特殊之处：当待定 \r 后紧跟非 \n 字节时，先返回 CRLine 确认该 \r，
// 此时调用方应再次以同一字节调用 Step（见 Flush 与 norm 的循环约定）。
func (d *Decoder) Step(b byte) (Event, error) {
	if d.flushed {
		return Other, ErrFlushed
	}
	if d.pendingCR {
		if b == '\n' {
			d.pendingCR = false
			return LFLine, nil // \r\n 作为一个行尾，\r 与 \n 均不进正文
		}
		d.pendingCR = false
		return CRLine, nil // 待定 \r 单独成行；b 尚未消费，调用方重放
	}
	if b == '\r' {
		d.pendingCR = true
		return PendingCR, nil
	}
	if b == '\n' {
		return LFLine, nil
	}
	return Other, nil
}

// Flush 在流结束时调用：若有悬着的 \r，则返回 CRLine 并终结解码器。
func (d *Decoder) Flush() (Event, error) {
	if d.flushed {
		return Other, ErrFlushed
	}
	d.flushed = true
	if d.pendingCR {
		d.pendingCR = false
		return CRLine, nil
	}
	return Other, nil
}
