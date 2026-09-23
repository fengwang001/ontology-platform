// Package u8 在字节级实现 UTF-8 的逐标量解码与编码。
// 依赖 scalar；不使用 unicode/utf8 及任何隐式字符串解码。
package u8

import "ontology/scalar"

// MaxPending 是切分缓存的硬上限：最长字符 4 字节，进入后至多再等 3 字节。
const MaxPending = 3

// Event 是状态机每消化一个单元吐出的结果。
type Event struct {
	R    scalar.Rune
	Len  int  // 本单元吞掉的输入字节数
	Bad  bool // 非法单元（替换模式映射为 U+FFFD）
	EOF  bool // Close 时残留的未完成前缀（截断）
}

// Decoder 是逐字节的 UTF-8 状态机。单次使用，非并发安全。
type Decoder struct {
	buf   [MaxPending]byte
	n     int // 已缓存字节数（0 表示空闲）
	need  int // 还需几个延续字节
	r     scalar.Rune
	lo    byte // 第二字节合法下界
	hi    byte // 第二字节合法上界
	check int64
}

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.check }

// Pending 返回当前缓存字节数（测试用于断言硬上限）。
func (d *Decoder) Pending() int { return d.n }

// Feed 送入一个字节。返回的 ev 非 nil 表示产出一个单元；
// replay 为 true 时同一字节必须原样再喂一次（它终结了前一单元，自身尚未解析）。
func (d *Decoder) Feed(b byte) (ev Event, replay bool) {
	d.check++
	if d.n == 0 {
		return d.start(b)
}
	lo, hi := byte(0x80), byte(0xBF)
	if d.n == 1 {
		lo, hi = d.lo, d.hi
	}
	if b < lo || b > hi {
		ev = Event{Len: d.n, Bad: true}
		d.n = 0
		return ev, true
	}
	d.r = d.r<<6 | scalar.Rune(b&0x3F)
	d.buf[d.n] = b
	d.n++
	d.need--
	if d.need == 0 {
		r := d.r
		n := d.n
		d.n = 0
		return Event{R: r, Len: n}, false
	}
	return Event{}, false
}

func (d *Decoder) start(b byte) (Event, bool) {
	switch {
	case b < 0x80:
		return Event{R: scalar.Rune(b), Len: 1}, false
	case b < 0xC2: // 80..BF 裸延续，C0..C1 非最短
		return Event{Len: 1, Bad: true}, false
	case b < 0xE0:
		d.arm(b&0x1F, 1, 0x80, 0xBF)
	case b == 0xE0:
		d.arm(b&0x0F, 2, 0xA0, 0xBF)
	case b < 0xED:
		d.arm(b&0x0F, 2, 0x80, 0xBF)
	case b == 0xED:
		d.arm(b&0x0F, 2, 0x80, 0x9F)
	case b < 0xF0:
		d.arm(b&0x0F, 2, 0x80, 0xBF)
	case b == 0xF0:
		d.arm(b&0x07, 3, 0x90, 0xBF)
	case b < 0xF4:
		d.arm(b&0x07, 3, 0x80, 0xBF)
	case b == 0xF4:
		d.arm(b&0x07, 3, 0x80, 0x8F)
	default: // F5..FF
		return Event{Len: 1, Bad: true}, false
	}
	d.buf[0] = b
	d.n = 1
	return Event{}, false
}

func (d *Decoder) arm(r scalar.Rune, need int, lo, hi byte) {
	d.r, d.need, d.lo, d.hi = r, need, lo, hi
}

// Flush 在流结束时调用：残留未完成前缀产出一个截断单元。
func (d *Decoder) Flush() (Event, bool) {
	if d.n == 0 {
		return Event{}, false
	}
	ev := Event{Len: d.n, Bad: true, EOF: true}
	d.n = 0
	return ev, true
}

// Encode 把标量编码为 UTF-8 追加到 dst。
func Encode(dst []byte, r scalar.Rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	}
}

// SplitTail 返回段尾必须移交给下一段的未完成前缀（最多 MaxPending 字节）。
func SplitTail(seg []byte) []byte {
	i := len(seg)
	for j := 0; j < MaxPending && i > 0; j++ {
		i--
		b := seg[i]
		if b < 0x80 { // ASCII 自成边界
			i++
			break
		}
		if b < 0xC0 {
			continue // 延续字节：继续向前找首字节
		}
		// b 是引导字节；只有其后延续字节数不足时才整体移交。
		if leadLen(b) > len(seg)-i {
			return seg[i:]
		}
		return nil
	}
	return nil
}

func leadLen(b byte) int {
	switch {
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}
