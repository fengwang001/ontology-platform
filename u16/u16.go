// Package u16 在字节级实现 UTF-16LE/BE 的流式解码与编码，不使用 unicode/utf16。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order uint8

const (
	LE Order = iota
	BE
)

// Event 类别。
type Event uint8

const (
	None Event = iota
	Rune
	Bad
)

// Decoder 逐字节解码；order 为配置序（BOM 检测在 stream 层）。零值需设 Order。
type Decoder struct {
	order Order
	half  int // 0 无半字节；1 已存一个字节
	first byte
	hi    rune // >=0 表示挂起高代理
}

// NewDecoder 以给定字节序构造解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o, hi: -1} }

// Step 喂入一个字节。返回事件、标量、是否消费。高代理后遇非低代理时，
// 定稿一个 Bad（孤立高代理），且 b 未消费（若它组成完整代码单元则整个单元退回）。
func (d *Decoder) Step(b byte) (Event, rune, bool) {
	if d.hi < 0 && d.half == 0 {
		d.first, d.half = b, 1
		return None, 0, true
	}
	var u rune
	if d.order == LE {
		u = rune(b)<<8 | rune(d.first)
	} else {
		u = rune(d.first)<<8 | rune(b)
	}
	d.half = 0
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi = u
		return None, 0, true
	case scalar.IsLowSurrogate(u):
		if d.hi >= 0 {
			r := scalar.SurrogatePair(d.hi, u)
			d.hi = -1
			return Rune, r, true
		}
		return Bad, 0, true // 孤立低代理
	default:
		if d.hi >= 0 { // 高代理后跟非低代理：定稿高代理，退回整个代码单元
			d.hi = -1
			d.first, d.half = 0, 0
			return Bad, 0, false
		}
		return Rune, u, true
	}
}

// Pending 返回挂起字节数：半代码单元记 1，半代码单元+高代理记 3。
func (d *Decoder) Pending() int {
	n := d.half
	if d.hi >= 0 {
		n += 2
	}
	return n
}

// Flush 流结束：无挂起 None，否则 Bad（截断）。
func (d *Decoder) Flush() Event {
	if d.Pending() == 0 {
		return None
	}
	d.half, d.hi = 0, -1
	return Bad
}

// Encode 把标量编码为指定字节序的 UTF-16 字节；非法标量返回 nil。
func Encode(r rune, o Order) []byte {
	if !scalar.Valid(r) {
		return nil
	}
	var units []rune
	if r < 0x10000 {
		units = []rune{r}
	} else {
		hi, lo := scalar.EncodeSurrogates(r)
		units = []rune{hi, lo}
	}
	out := make([]byte, 0, 2*len(units))
	for _, u := range units {
		if o == LE {
			out = append(out, byte(u), byte(u>>8))
		} else {
			out = append(out, byte(u>>8), byte(u))
		}
	}
	return out
}

// Handback 返回切点 cut（相对整流的字节偏移）处上段应交给下段的字节数（0..3）。
// p[:cut] 为上段全部内容；o 为已确定字节序。切点处挂起有三种：
// 奇数切点的半代码单元（1）；完整高代理（2）；半代码单元后接高代理（3）。
func Handback(p []byte, cut int, o Order) int {
	if cut%2 == 1 { // 半代码单元落在上段
		if cut >= 4 && isHighUnit(p, cut-3, o) && !followedByLow(p, cut, o) {
			return 3
		}
		return 1
	}
	if cut >= 2 && isHighUnit(p, cut-2, o) && !followedByLow(p, cut, o) {
		return 2
	}
	return 0
}

func followedByLow(p []byte, cut int, o Order) bool {
	if cut+1 >= len(p) {
		return false
	}
	var u rune
	if o == LE {
		u = rune(p[cut+1])<<8 | rune(p[cut])
	} else {
		u = rune(p[cut])<<8 | rune(p[cut+1])
	}
	return scalar.IsLowSurrogate(u)
}

func isHighUnit(p []byte, i int, o Order) bool {
	if i+1 >= len(p) {
		return false
	}
	var u rune
	if o == LE {
		u = rune(p[i+1])<<8 | rune(p[i])
	} else {
		u = rune(p[i])<<8 | rune(p[i+1])
	}
	return scalar.IsHighSurrogate(u)
}
