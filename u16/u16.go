// Package u16 逐字节实现 UTF-16LE/BE 解码状态机与编码，不使用 unicode/utf16。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order int

const (
	// LE 小端。
	LE Order = iota
	// BE 大端。
	BE
)

// Unit 是一次 UTF-16 解码裁决。
type Unit struct {
	R     rune
	Size  int  // 吞掉的输入字节数
	Bad   bool // 非法单元（孤立代理或非法对齐）
	Trunc bool // Close 时残留
}

// Decoder 是跨 Write 复用的 UTF-16 状态机。
type Decoder struct {
	ord   Order
	pend  bool // 缓存了半个码元
	first byte
	hi    rune // >=0 表示已收高代理，等待低代理
}

// NewDecoder 创建指定字节序的解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{ord: o, hi: -1} }

// Pending 返回缓存字节数（半个码元 1，或待配对的高代理 2）。
func (d *Decoder) Pending() int {
	n := 0
	if d.pend {
		n++
	}
	if d.hi >= 0 {
		n += 2
	}
	return n
}

// Feed 喂入一个字节；返回 false 时该字节需作为新码元首字节重喂。
func (d *Decoder) Feed(b byte) (Unit, bool) {
	if d.pend {
		d.pend = false
		u := rune(0)
		if d.ord == LE {
			u = rune(d.first) | rune(b)<<8
		} else {
			u = rune(d.first)<<8 | rune(b)
		}
		return d.onCodeUnit(u)
	}
	if d.hi >= 0 {
		// 这个字节属于一个新码元；先吐孤立高代理，字节留待重喂。
		u := Unit{Bad: true, Size: 2, R: d.hi}
		d.hi = -1
		return u, false
	}
	d.first, d.pend = b, true
	return Unit{}, true
}

func (d *Decoder) onCodeUnit(u rune) (Unit, bool) {
	if scalar.IsHighSurrogate(u) {
		d.hi = u
		return Unit{}, true
	}
	if d.hi >= 0 {
		if scalar.IsLowSurrogate(u) {
			r := scalar.FromSurrogatePair(d.hi, u)
			d.hi = -1
			return Unit{R: r, Size: 4}, true
		}
		d.hi = -1
		u2 := Unit{Bad: true, Size: 2}
		return u2, false // 当前码元作为新字符重喂
	}
	if scalar.IsLowSurrogate(u) {
		return Unit{Bad: true, Size: 2}, true
	}
	return Unit{R: u, Size: 2}, true
}

// Close 裁决残留：半个码元或孤立高代理均为截断。
func (d *Decoder) Close() (Unit, bool) {
	if d.pend {
		d.pend = false
		return Unit{Bad: true, Trunc: true, Size: 1}, true
	}
	if d.hi >= 0 {
		d.hi = -1
		return Unit{Bad: true, Trunc: true, Size: 2}, true
	}
	return Unit{}, true
}

// Encode 把标量编码成 UTF-16 字节；非法标量返回 nil。
func Encode(r rune, o Order) []byte {
	if !scalar.IsScalar(r) {
		return nil
	}
	units := []rune{r}
	if r >= 0x10000 {
		hi, lo := scalar.ToSurrogatePair(r)
		units = []rune{hi, lo}
	}
	out := make([]byte, 0, 4)
	for _, u := range units {
		if o == LE {
			out = append(out, byte(u), byte(u>>8))
		} else {
			out = append(out, byte(u>>8), byte(u))
		}
	}
	return out
}
