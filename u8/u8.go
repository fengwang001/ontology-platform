// Package u8 在字节级别解码/编码 UTF-8，禁止使用 unicode/utf8。
// 解码遵循「最长合法子部分」非法单元规则（见 DESIGN.md §1）。
package u8

import "ontology/scalar"

// 事件类型：Step 每次喂入一个字节后报告发生了什么。
const (
	evOK        = 0 // r 为合法标量，consumed 个字节构成该标量（含当前字节）
	evBad       = 1 // 当前字节构成一个长度 1 的非法单元，已消费
	evBadReplay = 2 // 已缓存的 num 字节构成一个非法单元，当前字节须重喂
	evNeed      = 3 // 字节被缓存，序列尚未结束，等待更多输入
)

// Decoder 是逐字节 UTF-8 解码器，非并发安全。
type Decoder struct {
	buf [3]byte // 未完成序列的前导字节，硬上限 3
	num int     // 缓存字节数 0..3
	rem int     // 还需要的续字节数
}

// reset 清空状态机。
func (d *Decoder) reset() { d.num, d.rem = 0, 0 }

// Pending 返回当前缓存中尚未决的字节数（硬上限 3）。
func (d *Decoder) Pending() int { return d.num }

func cont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// Step 喂入一个字节，返回事件、标量（仅 evOK/evPendingOK）、被本次事件
// 吞掉的字节数。evReplay 表示该字节未被消费，调用方必须重新喂给 Step。
func (d *Decoder) Step(b byte) (ev int, r rune, consumed int) {
	if d.num == 0 {
		switch {
		case b < 0x80:
			return evOK, rune(b), 1
		case b >= 0xC2 && b <= 0xDF:
			d.buf[0], d.num, d.rem = b, 1, 1
			return evNeed, 0, 1
		case b >= 0xE0 && b <= 0xEF:
			d.buf[0], d.num, d.rem = b, 1, 2
			return evNeed, 0, 1
		case b >= 0xF0 && b <= 0xF4:
			d.buf[0], d.num, d.rem = b, 1, 3
			return evNeed, 0, 1
		default: // 80..BF、C0 C1、F5..FF：长度 1 的非法单元
			return evBad, 0, 1
		}
	}
	first := d.buf[0]
	pos := d.num // 当前字节在序列中的位置（1=第二字节）
	switch {
	case pos == 1:
		switch first {
		case 0xE0:
			if b < 0xA0 || b > 0xBF {
				return d.failReplay()
			}
		case 0xED:
			if b < 0x80 || b > 0x9F {
				return d.failReplay()
			}
		case 0xF0:
			if b < 0x90 || b > 0xBF {
				return d.failReplay()
			}
		case 0xF4:
			if b < 0x80 || b > 0x8F {
				return d.failReplay()
			}
		default:
			if !cont(b) {
				return d.failReplay()
			}
		}
	default:
		if !cont(b) {
			return d.failReplay()
		}
	}
	d.buf[d.num] = b
	d.num++
	d.rem--
	if d.rem > 0 {
		return evNeed, 0, 1
	}
	r = decodeBuf(d.buf[:d.num])
	n := d.num
	d.reset()
	return evOK, r, n
}

// failReplay 在当前字节不满足位置区间时，把已缓存前缀判为一个非法单元，
// 当前字节不消费（调用方必须重新喂给 Step）。
func (d *Decoder) failReplay() (ev int, r rune, consumed int) {
	n := d.num
	d.reset()
	return evBadReplay, 0, n
}

func decodeBuf(p []byte) rune {
	switch len(p) {
	case 2:
		return rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		return rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	default:
		return rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 |
			rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
}

// Flush 在流结束时调用：若仍有未完成前缀，它是一个长度 num 的非法单元
// （替换模式输出一个 U+FFFD，严格模式视为截断）；返回 0 表示干净结束。
func (d *Decoder) Flush() (consumed int) {
	n := d.num
	d.reset()
	return n
}

// EncodeLen 返回标量 r 的 UTF-8 编码长度，非法标量按 U+FFFD 处理。
func EncodeLen(r rune) int {
	switch {
	case !scalar.IsScalar(r):
		return 3
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

// Encode 把标量写入 dst（容量由调用方按 EncodeLen 保证），返回写入字节数。
func Encode(dst []byte, r rune) int {
	if !scalar.IsScalar(r) {
		r = scalar.Replacement
	}
	switch {
	case r < 0x80:
		dst[0] = byte(r)
		return 1
	case r < 0x800:
		dst[0] = 0xC0 | byte(r>>6)
		dst[1] = 0x80 | byte(r)&0x3F
		return 2
	case r < 0x10000:
		dst[0] = 0xE0 | byte(r>>12)
		dst[1] = 0x80 | byte(r>>6)&0x3F
		dst[2] = 0x80 | byte(r)&0x3F
		return 3
	default:
		dst[0] = 0xF0 | byte(r>>18)
		dst[1] = 0x80 | byte(r>>12)&0x3F
		dst[2] = 0x80 | byte(r>>6)&0x3F
		dst[3] = 0x80 | byte(r)&0x3F
		return 4
	}
}
