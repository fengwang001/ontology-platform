// Package u8 实现 UTF-8 的逐标量增量解码与编码。
// 非法单元规则见 DESIGN.md 第 1 节；不重放 pending，切碎不回扫。
package u8

import "ontology/scalar"

// Unit 描述一次解码产出。
type Unit struct {
	R       rune
	Invalid bool
	BOM     bool // 整个流开头的 EF BB BF
	FromP   int
}

// Decoder 是 UTF-8 增量状态机，非并发安全。
type Decoder struct {
	pend [3]byte // 已缓存的未完成前缀
	n    int     // len(pend)，0..3
	acc  rune    // 已累积码位
	need int     // 还需续字节数

	Checks int

	bomDone bool
}

func (d *Decoder) PendingLen() int { return d.n }

func (d *Decoder) Pending() []byte { return append([]byte(nil), d.pend[:d.n]...) }

func (d *Decoder) Reset() { *d = Decoder{} }

// Feed 喂入字节；产出单元调用 emit（false=回滚该单元并停止）。
// 返回已确定离开调用方的字节数（不含留在 pending 中的字节）。
func (d *Decoder) Feed(p []byte, emit func(Unit) bool) int {
	n, cur := 0, 0 // n=已最终化且来自 p 的字节数；cur=当前单元来自 p 的字节数
	i := 0
	out := func(u Unit) bool {
		u.FromP = cur
		if !d.bomDone {
			d.bomDone = true
			if u.R == scalar.BOM && !u.Invalid {
				u.BOM = true
			}
		}
		if !emit(u) {
			return false
		}
		n += cur
		cur = 0
		return true
	}
	for i < len(p) {
		snap := *d
		snapCur := cur
		b := p[i]
		d.Checks++
		i++

		switch {
		case d.n == 0 && b <= 0x7F:
			cur = 1
			if !out(Unit{R: rune(b)}) {
				return n
			}
		case d.n == 0 && scalar.LeadLen(b) == 0:
			cur = 1
			if !out(Unit{R: scalar.Replacement, Invalid: true}) {
				return n
			}
		case d.n == 0:
			d.pend[0], d.n = b, 1
			d.acc, d.need = rune(b&(0xFF>>scalar.LeadLen(b))), scalar.LeadLen(b)-1
			cur = 1
		case d.n == 1:
			if !scalar.SecondOK(d.pend[0], b) {
				// 只吞首字节；当前字节退回，下一轮重新当首字节。
				*d = Decoder{Checks: d.Checks, bomDone: d.bomDone}
				i--
				if !out(Unit{R: scalar.Replacement, Invalid: true}) {
					return n
				}
				continue
			}
			d.pend[1], d.n = b, 2
			d.acc, d.need, cur = d.acc<<6|rune(b&0x3F), d.need-1, cur+1
			if d.need > 0 {
				continue
			}
			r := d.acc
			d.n, d.need, d.acc = 0, 0, 0
			if !out(Unit{R: r}) {
				*d = snap
				cur = snapCur
				return n
			}
		default: // n >= 2，续收阶段
			if scalar.IsCont8(b) {
				d.pend[d.n], d.n = b, d.n+1
				d.acc, d.need, cur = d.acc<<6|rune(b&0x3F), d.need-1, cur+1
				if d.need > 0 {
					continue
				}
				r := d.acc
				d.n, d.need, d.acc = 0, 0, 0
				if !out(Unit{R: r}) {
					*d = snap
					cur = snapCur
					return n
				}
			} else {
				// 残前缀整体为一个非法单元；当前字节退回。
				*d = Decoder{Checks: d.Checks, bomDone: d.bomDone}
				i--
				if !out(Unit{R: scalar.Replacement, Invalid: true}) {
					return n
				}
			}
		}
	}
	return n
}

// Finish 在流结束时调用：若残留未完成前缀，产出 1 个非法单元（截断）。
// 返回 false 表示 emit 拒绝（状态保持不变）。
func (d *Decoder) Finish(emit func(Unit) bool) bool {
	if d.n == 0 {
		return true
	}
	if !emit(Unit{R: scalar.Replacement, Invalid: true}) {
		return false
	}
	d.n, d.need, d.acc = 0, 0, 0
	return true
}

// Encode 把一个标量编码为 UTF-8（调用方需保证 r 是标量值）。
func Encode(r rune) []byte {
	switch {
	case r <= 0x7F:
		return []byte{byte(r)}
	case r <= 0x7FF:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r <= 0xFFFF:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F,
			0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
}
