// Package eol 识别行尾：\r\n、单独的 \r、\n，以及切分点处待定的 \r。
package eol

// Event 是喂入一个字节后产生的判定。
type Event uint8

const (
	// HoldCR 表示该字节是一个待定 \r：可能与下一字节 \n 配对。
	HoldCR Event = iota
	// LoneCR 表示待定 \r 被新字节确认成单独行尾（该 \r 不产生字节输出）。
	LoneCR
	// CRLF 表示 \r\n 配对成功（刚喂入的是 \n，两个原文字节都不直接输出）。
	CRLF
	// LF 表示单独的 \n。
	LF
	// Byte 表示普通字节（包括待定 \r 之前的字节）。
	Byte
)

// Decoder 是流式行尾识别器，零值可用，不跨 Byte 序列保留状态。
type Decoder struct {
	pending bool // 上一个字节是待定 \r
}

// Feed 喂入一个字节并返回该字节对应的事件。
//
// 当返回 LoneCR 时，新字节 b 的事件尚未给出，调用方必须再调用 Current(b)
// 取得它（通常是 Byte，除非 b == '\n'，但那种情况不会发生：\n 会配对成 CRLF）。
func (d *Decoder) Feed(b byte) Event {
	if d.pending {
		d.pending = false
		if b == '\n' {
			return CRLF
		}
		// 待定 \r 先行确认；调用方随后用 Current(b) 处理同一字节。
		return LoneCR
	}
	if b == '\r' {
		d.pending = true
		return HoldCR
	}
	if b == '\n' {
		return LF
	}
	return Byte
}

// Current 返回 LoneCR 之后被推迟判定的那个字节的事件。
func (d *Decoder) Current(b byte) Event {
	if b == '\r' {
		d.pending = true
		return HoldCR
	}
	if b == '\n' {
		return LF
	}
	return Byte
}

// Close 在流结束时调用：若有待定 \r，返回 true 表示它是单独行尾。
func (d *Decoder) Close() bool {
	if d.pending {
		d.pending = false
		return true
	}
	return false
}

// Pending 报告当前是否有一个待定 \r。
func (d *Decoder) Pending() bool { return d.pending }

// Reset 清空状态。
func (d *Decoder) Reset() { d.pending = false }
