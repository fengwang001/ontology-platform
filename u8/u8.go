// Package u8 提供 UTF-8 字节序列的逐标量解码与编码。
// 非法单元的切分遵循「最长合法前缀」规则（见 DESIGN.md 第 1 节）。
package u8

import "ontology/scalar"

// LeadLen 返回首字节对应的序列总长；0 表示非法首字节或游离延续字节。
func LeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 0
}

// IsCont 报告 b 是否为延续字节 (80..BF)。
func IsCont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// SecondRange 返回首字节 b 对应的第二字节合法区间（含端点）。
// 调用方保证 b 是合法多字节首字节。
func SecondRange(b byte) (lo, hi byte) {
	switch {
	case b == 0xE0:
		return 0xA0, 0xBF
	case b == 0xED:
		return 0x80, 0x9F
	case b == 0xF0:
		return 0x90, 0xBF
	case b == 0xF4:
		return 0x80, 0x8F
	}
	return 0x80, 0xBF
}

// DecodeOne 从 buf 开头解析一个单元，返回标量值 r、消费字节数 size、是否合法。
// 非法时 size 为「最长合法前缀」长度（1..L-1），即该非法单元吞掉的字节数；
// buf 截断在合法前缀中间时同样返回此前缀长度。
func DecodeOne(buf []byte) (r int32, size int, ok bool) {
	b0 := buf[0]
	l := LeadLen(b0)
	if l == 0 {
		return 0, 1, false
	}
	if l == 1 {
		return int32(b0), 1, true
	}
	lo, hi := SecondRange(b0)
	r = int32(b0) & (0x7F >> l)
	for i := 1; i < l; i++ {
		if i >= len(buf) {
			return 0, i, false
		}
		c := buf[i]
		if c < lo || c > hi {
			return 0, i, false
		}
		r = r<<6 | int32(c&0x3F)
		lo, hi = 0x80, 0xBF
	}
	return r, l, true
}

// AppendEncode 把合法标量 r 以 UTF-8 追加到 dst。
func AppendEncode(dst []byte, r int32) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
	return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
		0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
}

// AppendReplacement 追加 U+FFFD 的 UTF-8 编码。
func AppendReplacement(dst []byte) []byte {
	return AppendEncode(dst, scalar.Replacement)
}

// 回调约定：ok=true 表示合法标量 r（占 n 个输入字节）；ok=false 表示一个
// 吞掉 n 字节的非法单元。回调触发时缓存尚未弹出，调用方可结合 Pending()
// 推算该单元在整个流中的起始偏移。

// Decoder 是 UTF-8 的增量逐字节解码器；每个输入字节只被检查一次。
type Decoder struct{ pend []byte }

// NewDecoder 返回空的解码器。
func NewDecoder() *Decoder { return &Decoder{} }

// Pending 返回缓存中未决的字节数（硬上限 3，见 DESIGN.md 第 2 节）。
func (d *Decoder) Pending() int { return len(d.pend) }

// Feed 喂入一个字节；每完成一个单元触发一次回调（一个字节至多触发两次：
// 先报前缀非法单元，再把破坏字节作为新起点）。
func (d *Decoder) Feed(b byte, emit func(r int32, n int, ok bool)) {
	d.pend = append(d.pend, b)
	for {
		l := LeadLen(d.pend[0])
		if len(d.pend) == 1 {
			if l == 1 {
				emit(int32(b), 1, true)
			} else if l == 0 {
				emit(0, 1, false)
			}
			if l <= 1 {
				d.pend = d.pend[:0]
			}
			return
		}
		lo, hi := byte(0x80), byte(0xBF)
		if len(d.pend) == 2 {
			lo, hi = SecondRange(d.pend[0])
		}
		if c := d.pend[len(d.pend)-1]; c >= lo && c <= hi {
			if len(d.pend) < l {
				return
			}
			r, _, _ := DecodeOne(d.pend)
			emit(r, l, true)
			d.pend = d.pend[:0]
			return
		}
		last := d.pend[len(d.pend)-1]
		emit(0, len(d.pend)-1, false)
		d.pend = append(d.pend[:0], last)
	}
}

// Flush 把残留的合法前缀作为一个非法单元上报并清空（流结束截断）。
func (d *Decoder) Flush(emit func(r int32, n int, ok bool)) {
	if len(d.pend) > 0 {
		emit(0, len(d.pend), false)
		d.pend = d.pend[:0]
	}
}
