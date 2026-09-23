// Package u16 实现 UTF-16LE/UTF-16BE 的逐标量增量解码与编码。
// 代理对之外的码元独立成单元；孤立/错位代理按 DESIGN.md 处理。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order int

const (
	LE Order = iota
	BE
	// Auto 只允许出现在整个流的第 0 段：用开头 FEFF/FFFE 定序，缺省 LE。
	Auto
)

// Unit 描述一次解码产出。
type Unit struct {
	R       rune
	Invalid bool // 孤立代理
	BOM     bool // 开头用于定序的 FEFF
	FromP   int  // 该单元从本次 Feed 入参 p 中新吞的字节数
}

// Decoder 是 UTF-16 增量状态机，非并发安全。
type Decoder struct {
	order    Order
	orderSet bool

	pend [3]byte // 未完成前缀：高代理(2)，可能再带 1 个奇数字节
	hi   rune   // >=0 表示有高代理
	odd  bool   // 在高代理之外是否还残留 1 个奇数字节

	Checks int // 字节作为“新字节”进入状态机被检查的总次数
}

// NewDecoder 创建解码器；order 为 LE/BE/Auto。
func NewDecoder(order Order) *Decoder { return &Decoder{order: order, hi: -1} }

// Reset 清空全部状态。
func (d *Decoder) Reset() { *d = Decoder{order: d.order, hi: -1} }

// PendingLen 返回缓存字节数（硬上限 3）。
func (d *Decoder) PendingLen() int {
	n := 0
	if d.hi >= 0 {
		n += 2
	}
	if d.odd {
		n++
	}
	return n
}

// Pending 返回缓存前缀副本（供 par 段间交接）。
func (d *Decoder) Pending() []byte { return append([]byte(nil), d.pend[:d.PendingLen()]...) }

func (d *Decoder) codeUnit(b0, b1 byte) rune {
	if d.order == BE {
		return rune(b0)<<8 | rune(b1)
	}
	return rune(b1)<<8 | rune(b0)
}

// Feed 喂入字节；产出单元调用 emit（false=回滚并停止）。返回离开调用方的字节数。
func (d *Decoder) Feed(p []byte, emit func(Unit) bool) int {
	n, i := 0, 0 // n=已最终化且来自 p 的字节数
	cur := 0     // 当前单元来自 p 的字节数
	out := func(u Unit) bool {
		u.FromP = cur
		if !emit(u) {
			return false
		}
		n += cur
		cur = 0
		return true
	}
	for i+1 < len(p) {
		snap := *d
		snapCur := cur
		d.Checks += 2
		b0, b1 := p[i], p[i+1]

		if d.order == Auto && !d.orderSet {
			switch {
			case b0 == 0xFE && b1 == 0xFF:
				d.orderSet = true
				d.order = BE
				i += 2
				cur = 2
				if !out(Unit{R: scalar.BOM, BOM: true}) {
					return n
				}
				continue
			case b0 == 0xFF && b1 == 0xFE:
				d.orderSet = true
				d.order = LE
				i += 2
				cur = 2
				if !out(Unit{R: scalar.BOM, BOM: true}) {
					return n
				}
				continue
			default:
				d.orderSet = true
				d.order = LE
				continue // 不消费，按确定后的字节序重处理这两个字节
			}
		}

		u := d.codeUnit(b0, b1)
		i += 2
		switch {
		case d.hi >= 0 && scalar.IsLowSurrogate(u):
			r := scalar.DecodeSurrogate(d.hi, u)
			d.hi = -1
			cur += 2
			if !out(Unit{R: r}) {
				*d = snap
				cur = snapCur
				return n
			}
		case d.hi >= 0:
			// 孤立高代理出 FFFD；当前码元退回重新处理，绝不一起吞掉。
			d.hi = -1
			i -= 2
			if !out(Unit{R: scalar.Replacement, Invalid: true}) {
				*d = snap
				cur = snapCur
				return n
			}
		case scalar.IsHighSurrogate(u):
			d.hi = u
			d.pend[0], d.pend[1] = b0, b1
			cur += 2
		case scalar.IsLowSurrogate(u):
			cur += 2
			if !out(Unit{R: scalar.Replacement, Invalid: true}) {
				*d = snap
				cur = snapCur
				return n
			}
		default:
			cur += 2
			if !out(Unit{R: u}) {
				*d = snap
				cur = snapCur
				return n
			}
		}
	}
	// 剩 1 个奇数字节：缓存，不算已消费；若已有高代理，排在它之后。
	if i < len(p) {
		d.Checks++
		idx := 0
		if d.hi >= 0 {
			idx = 2
		}
		d.pend[idx] = p[i]
		d.odd = true
	}
	return n
}

// Finish 在流结束时调用：残留奇数字节或高代理都属截断；false 表示 emit 拒绝。
func (d *Decoder) Finish(emit func(Unit) bool) bool {
	if d.odd {
		if !emit(Unit{R: scalar.Replacement, Invalid: true}) {
			return false
		}
		d.odd = false
	}
	if d.hi >= 0 {
		if !emit(Unit{R: scalar.Replacement, Invalid: true}) {
			return false
		}
		d.hi = -1
	}
	return true
}

// Encode 把标量编码为指定字节序的 UTF-16；r>=0x10000 编成代理对。
func Encode(r rune, order Order) []byte {
	put := func(cu rune) []byte {
		if order == BE {
			return []byte{byte(cu >> 8), byte(cu)}
		}
		return []byte{byte(cu), byte(cu >> 8)}
	}
	if r >= 0x10000 {
		hi, lo := scalar.EncodeSurrogate(r)
		return append(put(hi), put(lo)...)
	}
	return put(r)
}
