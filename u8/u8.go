// Package u8 是手写的字节级 UTF-8 解码器/编码器，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// Kind 标识一个解码单元的类型。
type Kind uint8

const (
	Scalar Kind = iota // 合法标量
	Bad                // 非法单元
	Trunc              // 流结束时残留的合法前缀（截断）
)

// Unit 是一次解码判决：一个标量或一个非法单元。
type Unit struct {
	Kind  Kind
	Rune  scalar.Rune
	Start int // 单元在整个输入流中的起始字节偏移
	Len   int // 该单元吞掉的输入字节数
}

// Decoder 是有状态的增量 UTF-8 解码器，可跨 Write 保留半字符。
type Decoder struct {
	total             int // 期望总长：2/3/4；0 表示空闲
	got               int // 已接受的继续字节数
	r                 scalar.Rune
	start             int
	min2, max2        byte
	checks            int64
}

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Feed 喂入一个字节（abs 为其绝对偏移），返回 0~2 个判决单元。
func (d *Decoder) Feed(b byte, abs int) (u1 Unit, u2 Unit, n int) {
	d.checks++
	if d.total == 0 {
		return d.lead(b, abs)
	}
	if b < 0x80 || b > 0xBF {
		u := Unit{Kind: Bad, Start: d.start, Len: 1 + d.got}
		d.total, d.got, d.r = 0, 0, 0
		u2, _, n = d.lead(b, abs)
		return u, u2, 1 + n
	}
	if d.got == 1 && (b < d.min2 || b > d.max2) {
		u := Unit{Kind: Bad, Start: d.start, Len: 1}
		d.total, d.got, d.r = 0, 0, 0
		u2, _, n = d.lead(b, abs)
		return u, u2, 1 + n
	}
	d.r = d.r<<6 | scalar.Rune(b&0x3F)
	d.got++
	if d.got+1 == d.total {
		u := Unit{Kind: Scalar, Rune: d.r, Start: d.start, Len: d.total}
		d.total, d.got, d.r = 0, 0, 0
		return u, Unit{}, 1
	}
	return Unit{}, Unit{}, 0
}

func (d *Decoder) lead(b byte, abs int) (Unit, Unit, int) {
	d.checks++
	switch {
	case b < 0x80:
		return Unit{Kind: Scalar, Rune: scalar.Rune(b), Start: abs, Len: 1}, Unit{}, 1
	case b >= 0xC2 && b <= 0xDF:
		d.total, d.got, d.r, d.start = 2, 0, scalar.Rune(b & 0x1F), abs
		d.min2, d.max2 = 0x80, 0xBF
	case b == 0xE0:
		d.total, d.got, d.r, d.start = 3, 0, scalar.Rune(b&0x0F), abs
		d.min2, d.max2 = 0xA0, 0xBF
	case b >= 0xE1 && b <= 0xEC:
		d.total, d.got, d.r, d.start = 3, 0, scalar.Rune(b&0x0F), abs
		d.min2, d.max2 = 0x80, 0xBF
	case b == 0xED:
		d.total, d.got, d.r, d.start = 3, 0, scalar.Rune(b&0x0F), abs
		d.min2, d.max2 = 0x80, 0x9F
	case b >= 0xEE && b <= 0xEF:
		d.total, d.got, d.r, d.start = 3, 0, scalar.Rune(b&0x0F), abs
		d.min2, d.max2 = 0x80, 0xBF
	case b == 0xF0:
		d.total, d.got, d.r, d.start = 4, 0, scalar.Rune(b&0x07), abs
		d.min2, d.max2 = 0x90, 0xBF
	case b >= 0xF1 && b <= 0xF3:
		d.total, d.got, d.r, d.start = 4, 0, scalar.Rune(b&0x07), abs
		d.min2, d.max2 = 0x80, 0xBF
	case b == 0xF4:
		d.total, d.got, d.r, d.start = 4, 0, scalar.Rune(b&0x07), abs
		d.min2, d.max2 = 0x80, 0x8F
	default:
		return Unit{Kind: Bad, Start: abs, Len: 1}, Unit{}, 1
	}
	return Unit{}, Unit{}, 0
}

// Finish 在流结束时调用：残留合法前缀作为一个截断单元返回。
func (d *Decoder) Finish() (Unit, bool) {
	if d.total == 0 {
		return Unit{}, false
	}
	u := Unit{Kind: Trunc, Start: d.start, Len: 1 + d.got}
	d.total, d.got, d.r = 0, 0, 0
	return u, true
}

// Pending 返回当前缓存的半成品字节数（硬上限 3）。
func (d *Decoder) Pending() int {
	if d.total == 0 {
		return 0
	}
	return 1 + d.got
}

// Encode 把一个合法标量编码为 UTF-8 字节。
func Encode(r scalar.Rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F),
			0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	}
}
