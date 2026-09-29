// Package u16 是手写的字节级 UTF-16LE/BE 解码器/编码器，不使用 unicode/utf16。
package u16

import "ontology/scalar"

type Kind uint8

const (
	Scalar Kind = iota
	Bad
	Trunc
)

// Order 指定 UTF-16 字节序。
type Order uint8

const (
	LE Order = iota
	BE
)

// Unit 是一次 UTF-16 解码判决。
type Unit struct {
	Kind  Kind
	Rune  scalar.Rune
	BOM   bool // 流首的 U+FEFF，用于 BOM 处理
	Start int
	Len   int // 字节数：标量 2，代理对 4，孤立代理/奇字节 2/1
}

// Decoder 是增量 UTF-16 解码器；order 为 nil 表示等 BOM 自动判定。
type Decoder struct {
	order    Order
	auto     bool
	have     bool
	hi       scalar.Rune
	hiStart  int
	odd      byte
	checks   int64
	first    bool
}

// NewDecoder 创建解码器；auto 为 true 时由流首 BOM 定序，缺省按 LE。
func NewDecoder(o Order, auto bool) *Decoder {
	return &Decoder{order: o, auto: auto, first: true}
}

func (d *Decoder) Checks() int64 { return d.checks }

func (d *Decoder) unit16(x uint16) uint16 { return x }

// Feed 喂入一个字节，返回 0~1 个判决单元。
func (d *Decoder) Feed(b byte, abs int) (Unit, bool) {
	d.checks++
	if !d.have {
		d.odd, d.have, d.hiStart = b, true, abs
		return Unit{}, false
	}
	d.have = false
	var x uint16
	if d.order == LE {
		x = uint16(d.odd) | uint16(b)<<8
	} else {
		x = uint16(d.odd)<<8 | uint16(b)
	}
	r := scalar.Rune(x)
	if d.first {
		d.first = false
		if r == scalar.BOM {
			return Unit{Kind: Scalar, Rune: r, BOM: true, Start: d.hiStart, Len: 2}, true
		}
	}
	if scalar.HighSurrogate(r) {
		d.hi, d.hiStart, d.have = r, d.hiStart, false
		d.hiPending = true
		return Unit{}, false
	}
	if d.hiPending {
		d.hiPending = false
		// 高代理后非低代理：高代理成非法单元；当前码元重新解析。
		u := Unit{Kind: Bad, Start: d.hiStart, Len: 2}
		d.refeed(r, d.hiStart+2)
		return u, true
	}
	if scalar.LowSurrogate(r) {
		return Unit{Kind: Bad, Rune: r, Start: d.hiStart, Len: 2}, true
	}
	return Unit{Kind: Scalar, Rune: r, Start: d.hiStart, Len: 2}, true
}

func (d *Decoder) refeed(r scalar.Rune, abs int) {
	// 重新解析高代理之后的码元：仅可能是低代理（非法）或普通标量。
	d.refed = true
	d.refedR, d.refedStart = r, abs
}

// Finish 在流结束时调用：奇字节或孤立高代理均为截断。
func (d *Decoder) Finish() (Unit, bool) {
	if d.have {
		d.have = false
		return Unit{Kind: Trunc, Start: d.hiStart, Len: 1}, true
	}
	if d.hiPending {
		d.hiPending = false
		return Unit{Kind: Trunc, Start: d.hiStart, Len: 2}, true
	}
	return Unit{}, false
}

// Pending 返回缓存字节数（硬上限 3：奇字节 + 孤立高代理）。
func (d *Decoder) Pending() int {
	n := 0
	if d.have {
		n++
	}
	if d.hiPending {
		n += 2
	}
	return n
}

// Encode 把标量编码成指定字节序的 UTF-16 字节（辅助平面为代理对）。
func Encode(r scalar.Rune, o Order) []byte {
	var units []uint16
	if hi, lo, ok := scalar.SplitSurrogate(r); ok {
		units = []uint16{uint16(hi), uint16(lo)}
	} else {
		units = []uint16{uint16(r)}
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
