// Package u16 用自写逻辑解码与编码 UTF-16LE / UTF-16BE 字节流。
// 禁止使用 unicode/utf16；代理对组合完全自行完成。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order uint8

const (
	LE Order = iota
	BE
)

// Kind 与 u8.Kind 同构：合法标量或非法单元。
type Kind uint8

const (
	Scalar Kind = iota
	Illegal
)

// Event 是一个 UTF-16 解码事件；Size 为吞掉的字节数。
type Event struct {
	Kind Kind
	R    rune
	Size int
}

// Decoder 是增量 UTF-16 解码器。order 为 nil 时等流首 BOM 定序。
type Decoder struct {
	order  *Order
	hi     rune // 0 表示无缓存的高代理
	odd    byte // 残留的奇数尾字节
	checks int64
}

// NewDecoder 创建解码器；传 nil 表示由流首 BOM 自动判定（无 BOM 视为 LE）。
func NewDecoder(o *Order) *Decoder { return &Decoder{order: o} }

// Checks 返回被检查的字节总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Order 返回当前生效字节序（自动定序前返回 LE,false）。
func (d *Decoder) Order() (Order, bool) {
	if d.order == nil {
		return LE, false
	}
	return *d.order, true
}

// Pending 返回尚未判定的缓存字节数（0/1/2，硬上限 2）。
func (d *Decoder) Pending() int {
	n := 0
	if d.odd != 0 {
		n = 1
	}
	if d.hi != 0 {
		n = 2
	}
	return n
}

func (d *Decoder) unit(hi, lo byte) rune {
	if d.order != nil && *d.order == BE {
		return rune(hi)<<8 | rune(lo)
	}
	return rune(lo)<<8 | rune(hi)
}

// Feed 喂入字节，对每个事件回调 fn。
func (d *Decoder) Feed(p []byte, fn func(Event)) {
	i := 0
	if d.odd != 0 && len(p) > 0 {
		d.checks++
		b0, b1 := d.odd, p[0]
		d.odd = 0
		i = 1
		d.consumeUnit(b0, b1, fn)
	}
	for ; i+1 < len(p); i += 2 {
		d.checks += 2
		d.consumeUnit(p[i], p[i+1], fn)
	}
	if i < len(p) {
		d.checks++
		d.odd = p[i]
	}
}

func (d *Decoder) consumeUnit(b0, b1 byte, fn func(Event)) {
	u := d.unit(b0, b1)
	if d.order == nil {
		o := LE
		if u == 0xFEFF {
			o = BE
		}
		d.order = &o
		if u == 0xFEFF || u == 0xFFFE {
			return // 流首 BOM 被吞掉（FFFE 在 LE 解释下也是 BOM 标记）
		}
	}
	if d.hi != 0 {
		if scalar.IsLowSurrogate(u) {
			fn(Event{Scalar, scalar.CombineSurrogates(d.hi, u), 4})
			d.hi = 0
			return
		}
		fn(Event{Illegal, scalar.Replacement, 2}) // 孤立高代理
		d.hi = 0
		// 非低代理单元不得被吞：落到下面重新作为新字符处理。
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi = u
	case scalar.IsLowSurrogate(u):
		fn(Event{Illegal, scalar.Replacement, 2})
	case scalar.IsScalar(u):
		fn(Event{Scalar, u, 2})
	default:
		fn(Event{Illegal, scalar.Replacement, 2})
	}
}

// Truncated 在流结束时报告截断残留字节数（奇数尾字节或孤立高代理）。
func (d *Decoder) Truncated() int {
	n := 0
	if d.odd != 0 {
		n++
		d.odd = 0
	}
	if d.hi != 0 {
		n += 2
		d.hi = 0
	}
	return n
}

// AlignEven 供 par 使用：返回切点应向前回退的字节数（0 或 1）使其落在偶边界。
func AlignEven(off int) int { return off & 1 }

// Encode 把一个标量按 o 编码追加到 dst；大于 U+FFFF 编成代理对。
func Encode(dst []byte, r rune, o Order) []byte {
	if r < 0x10000 {
		return append(dst, encUnit(r, o)...)
	}
	hi, lo := scalar.SplitSupplementary(r)
	dst = append(dst, encUnit(hi, o)...)
	return append(dst, encUnit(lo, o)...)
}

func encUnit(u rune, o Order) []byte {
	if o == BE {
		return []byte{byte(u >> 8), byte(u)}
	}
	return []byte{byte(u), byte(u >> 8)}
}

// BOMBytes 返回指定字节序的 BOM 字节。
func BOMBytes(o Order) []byte { return encUnit(scalar.BOM, o) }
