// Package u16 在字节级别解码/编码 UTF-16LE 与 UTF-16BE，禁止使用
// unicode/utf16。孤立代理按非法单元处理（见 DESIGN.md 代理规则）。
package u16

import "ontology/scalar"

// Order 标识 UTF-16 字节序。
type Order int

const (
	LE   Order = iota // 小端
	BE                // 大端
	Auto              // 由流首 BOM 决定；无 BOM 时按 LE
)

// 事件类型（与 u8 同构；replay 携带需要重新喂入的字节）。
const (
	evOK        = 0 // r 合法，consumed 为本单元字节数（2 或 4）
	evBad       = 1 // 当前代码单元是孤立低代理，consumed=2
	evBadReplay = 2 // 高代理未配对：consumed=2，replay 为后续 2 字节
	evNeed      = 3 // 收到奇数位置的一个字节，等待配对
)

// Decoder 逐字节解码 UTF-16；hi>=0 表示已缓存一个高代理代码单元。
type Decoder struct {
	order      Order
	flip       bool // Auto 模式下尚未确定字节序
	pending    byte // 奇数残字节（硬上限 1）
	hasPending bool
	hi         rune // 待配对的高代理，<0 表示无
}

// NewDecoder 按给定字节序构造解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o, hi: -1, flip: o == Auto} }

// Order 返回当前生效字节序（Auto 模式在首代码单元后被确定）。
func (d *Decoder) Order() Order { return d.order }

// Pending 返回当前缓存字节数（0 或 1）。
func (d *Decoder) Pending() int {
	n := 0
	if d.hasPending {
		n++
	}
	return n
}

func (d *Decoder) unit(lo, hi byte) rune {
	if d.order == BE {
		return rune(hi)<<8 | rune(lo)
	}
	return rune(lo)<<8 | rune(hi)
}

// Step 喂入一个字节。evBadReplay 时 replay 是未被消费、须重新喂入的字节。
func (d *Decoder) Step(b byte) (ev int, r rune, consumed int, replay []byte) {
	if !d.hasPending {
		d.pending, d.hasPending = b, true
		return evNeed, 0, 1, nil
	}
	d.hasPending = false
	u := d.unit(d.pending, b)
	if d.flip { // 仅流首第一个代码单元参与字节序判定
		d.flip = false
		switch u {
		case 0xFEFF:
			d.order = LE
			return evOK, scalar.BOM, 2, nil
		case 0xFFFE:
			d.order = BE
			return evOK, scalar.BOM, 2, nil
		}
		d.order = LE // 无 BOM：默认 LE（BE 输入应显式指定）
	}
	if d.hi >= 0 {
		if scalar.IsLowSurrogate(u) {
			high := d.hi
			d.hi = -1
			return evOK, scalar.FromSurrogatePair(high, u), 4, nil
		}
		// 高代理后跟「非低代理」：高代理是非法单元，当前代码单元必须
		// 重新作为新字符处理（不能被一起吞掉）。
		d.hi = -1
		if scalar.IsHighSurrogate(u) {
			d.hi = u
			return evBadReplay, 0, 2, []byte{d.pending, b}
		}
		return evOK, u, 2, nil
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi = u
		return evNeed, 0, 2, nil
	case scalar.IsLowSurrogate(u):
		return evBad, 0, 2, nil
	default:
		return evOK, u, 2, nil
	}
}

// Flush 报告流结束时的残留：返回 >0 表示存在截断（奇数残字节或孤立高
// 代理），值为残留字节数；替换模式对其输出一个 U+FFFD。
func (d *Decoder) Flush() int {
	n := 0
	if d.hasPending {
		n++
	}
	if d.hi >= 0 {
		n += 2
	}
	d.hasPending, d.hi = false, -1
	return n
}

// Encode 把标量按字节序编码追加到 dst，返回新切片；非法标量按 U+FFFD。
func Encode(dst []byte, o Order, r rune) []byte {
	if !scalar.IsScalar(r) {
		r = scalar.Replacement
	}
	if r < 0x10000 {
		return appendUnit(dst, o, uint16(r))
	}
	hi, lo := scalar.ToSurrogatePair(r)
	dst = appendUnit(dst, o, uint16(hi))
	return appendUnit(dst, o, uint16(lo))
}

func appendUnit(dst []byte, o Order, u uint16) []byte {
	lo, hi := byte(u), byte(u>>8)
	if o == BE {
		return append(dst, hi, lo)
	}
	return append(dst, lo, hi)
}
