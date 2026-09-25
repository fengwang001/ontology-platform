// Package u8 手工实现 UTF-8 逐标量解码/编码，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// Unit 是一次解码结果：一个合法标量或一个非法单元。
type Unit struct {
	R       rune
	Start   int // 单元首字节在整个输入流中的偏移
	Size    int // 单元吞掉的字节数
	Illegal bool
	Trunc   bool // 仅因 EOF 截断而非法（真前缀）
}

// Decoder 是有状态 UTF-8 解码器；单实例非并发安全。
type Decoder struct {
	need, k int
	r       rune
	start   int
	lo2     byte
	hi2     byte
	replay  bool
}

// Pending 返回缓存中尚未成单元的字节数（上限 3）。
func (d *Decoder) Pending() int { return d.k }

// Replay 报告上一次 Push 的字节需要以同一偏移再次 Push。
func (d *Decoder) Replay() bool { return d.replay }

// Push 喂入一个绝对偏移为 pos 的字节；单元完成时返回 ok=true。
func (d *Decoder) Push(b byte, pos int) (u Unit, ok bool) {
	d.replay = false
	if d.k == 0 {
		switch {
		case b < 0x80:
			return Unit{R: rune(b), Start: pos, Size: 1}, true
		case b >= 0xC2 && b <= 0xDF:
			d.need, d.lo2, d.hi2 = 2, 0x80, 0xBF
			d.r = rune(b & 0x1F)
		case b == 0xE0:
			d.need, d.lo2, d.hi2 = 3, 0xA0, 0xBF
			d.r = rune(b & 0x0F)
		case b >= 0xE1 && b <= 0xEC:
			d.need, d.lo2, d.hi2 = 3, 0x80, 0xBF
			d.r = rune(b & 0x0F)
		case b == 0xED:
			d.need, d.lo2, d.hi2 = 3, 0x80, 0x9F
			d.r = rune(b & 0x0F)
		case b >= 0xEE && b <= 0xEF:
			d.need, d.lo2, d.hi2 = 3, 0x80, 0xBF
			d.r = rune(b & 0x0F)
		case b == 0xF0:
			d.need, d.lo2, d.hi2 = 4, 0x90, 0xBF
			d.r = rune(b & 0x07)
		case b >= 0xF1 && b <= 0xF3:
			d.need, d.lo2, d.hi2 = 4, 0x80, 0xBF
			d.r = rune(b & 0x07)
		case b == 0xF4:
			d.need, d.lo2, d.hi2 = 4, 0x80, 0x8F
			d.r = rune(b & 0x07)
		default: // 80..BF、C0 C1、F5..FF
			return Unit{R: scalar.RuneError, Start: pos, Size: 1, Illegal: true}, true
		}
		d.k, d.start = 1, pos
		return Unit{}, false
	}
	if b < 0x80 || b > 0xBF { // 非续元：缓存整体作非法单元，b 重解析
		u = Unit{R: scalar.RuneError, Start: d.start, Size: d.k, Illegal: true}
		d.k, d.replay = 0, true
		return u, true
	}
	if d.k == 1 && (b < d.lo2 || b > d.hi2) { // 第二字节越界：只吞首字节
		u = Unit{R: scalar.RuneError, Start: d.start, Size: 1, Illegal: true}
		d.k, d.replay = 0, true
		return u, true
	}
	d.k++
	d.r = d.r<<6 | rune(b&0x3F)
	if d.k < d.need {
		return Unit{}, false
	}
	u, d.k = Unit{R: d.r, Start: d.start, Size: d.need}, 0
	return u, true
}

// Close 在流结束时调用；残留真前缀作为截断非法单元返回。
func (d *Decoder) Close() (u Unit, ok bool) {
	if d.k == 0 {
		return Unit{}, false
	}
	u = Unit{R: scalar.RuneError, Start: d.start, Size: d.k, Illegal: true, Trunc: true}
	d.k = 0
	return u, true
}

// Len 返回标量 r 的 UTF-8 编码长度。
func Len(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// Encode 把合法标量编码为 UTF-8。
func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F, 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
}
