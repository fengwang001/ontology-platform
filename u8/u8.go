// Package u8 实现 UTF-8 的逐标量增量解码与编码。
// 依赖仅 scalar；禁止使用 unicode/utf8 等现成解码。
package u8

import "ontology/scalar"

// Unit 是一次解码判定。
type Unit struct {
	R        rune // 合法标量；非法时为 U+FFFD
	OK       bool // 是否为合法标量
	Consumed int  // 本次判定吞掉的输入字节数（1..4）
	Incomp   bool // 到流结束仍是合法但不完整的前缀
}

// Dec 是增量 UTF-8 解码器：内部缓存至多 3 字节。
type Dec struct {
	buf [3]byte
	n   int
	val rune
	min rune
	rem int
	chk int // 字节被检查的总次数
}

// Checks 返回字节被检查的总次数。
func (d *Dec) Checks() int { return d.chk }

// Pending 返回尚未定案（暂存）的字节数，上限 3。
func (d *Dec) Pending() int { return d.n }

func isCont(b byte) bool { return b&0xC0 == 0x80 }

// IsCont 报告 b 是否为 10xxxxxx 继续字节。
func IsCont(b byte) bool { return isCont(b) }

// LeadLen 返回首字节声称的序列长度；非法首字节返回 0。
func LeadLen(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	default:
		return 0
	}
}

// secondOK 是首字节对应的第二字节合法区间。
func secondOK(first, b byte) bool {
	switch {
	case first == 0xE0:
		return b >= 0xA0 && b <= 0xBF
	case first == 0xED:
		return b >= 0x80 && b <= 0x9F
	case first == 0xF0:
		return b >= 0x90 && b <= 0xBF
	case first == 0xF4:
		return b >= 0x80 && b <= 0x8F
	default:
		return isCont(b)
	}
}

// Step 喂入下一字节：可返回 0、1 或 2 个判定（终止字节回流产生 2 个）。
func (d *Dec) Step(b byte) []Unit {
	d.chk++
	if d.n == 0 {
		return d.fresh(b)
	}
	ok := isCont(b)
	if ok && d.n == 1 {
		ok = secondOK(d.buf[0], b)
	}
	if !ok {
		u := Unit{R: scalar.Replacement, Consumed: d.n}
		d.n = 0
		return append([]Unit{u}, d.fresh(b)...)
	}
	d.val = d.val<<6 | rune(b&0x3F)
	d.n++
	d.rem--
	if d.rem > 0 {
		return nil
	}
	r, n := d.val, d.n
	d.n = 0
	return []Unit{{R: r, OK: true, Consumed: n}}
}

// fresh 处理无前缀时的一个首字节。
func (d *Dec) fresh(b byte) []Unit {
	switch {
	case b < 0x80:
		return []Unit{{R: rune(b), OK: true, Consumed: 1}}
	case b < 0xC2: // 80..BF 游离、C0 C1
		return []Unit{{R: scalar.Replacement, Consumed: 1}}
	case b > 0xF4: // F5..FF
		return []Unit{{R: scalar.Replacement, Consumed: 1}}
	}
	ln := LeadLen(b)
	d.buf[0], d.n, d.rem = b, 1, ln-1
	mask := byte(0x1F)
	if ln == 3 {
		mask = 0x0F
	} else if ln == 4 {
		mask = 0x07
	}
	d.val = rune(b & mask)
	return nil
}

// Flush 结束流：残留前缀（若有）作为一个非法单元，Consumed 为其字节数。
func (d *Dec) Flush() (u Unit, ok bool) {
	if d.n == 0 {
		return Unit{}, false
	}
	u = Unit{R: scalar.Replacement, Consumed: d.n, Incomp: true}
	d.n = 0
	return u, true
}

// EncodeAppend 把标量编码为 UTF-8 追加到 out。
func EncodeAppend(out []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(out, byte(r))
	case r < 0x800:
		return append(out, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(out, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(out, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
