// Package u8 在字节级实现 UTF-8 逐标量解码与编码，依赖 scalar。
package u8

import "ontology/scalar"

// Unit 是一次解码结果：rune 有效表示一个标量；illegal 为 true 表示一个
// 非法单元（rune==U+FFFD）；eof 为 true 表示 Close 时残留的合法前缀被截断。
type Unit struct {
	Rune    rune
	Illegal bool
	EOF      bool
	Len      int
}

// Decoder 是字节级 UTF-8 状态机。pending 永远是合法前缀，长度 ≤3。
type Decoder struct {
	pend []byte
}

func NewDecoder() *Decoder { return &Decoder{pend: make([]byte, 0, 3) }

// Pending 返回尚未形成单元的合法前缀（长度 0..3）。
func (d *Decoder) Pending() []byte { return d.pend }

// Prime 仅推进状态而不产出单元，用于并行段的静默引导。
func (d *Decoder) Prime(b byte) {
	if len(d.pend) == 0 {
		if class(b) == lead1 {
			d.pend = append(d.pend, b)
		}
		return
	}
	if b&0xC0 == 0x80 && validCont(d.pend, b) {
		d.pend = append(d.pend, b)
	} else {
		d.pend = d.pend[:0]
		if class(b) == lead1 {
			d.pend = append(d.pend, b)
		}
	}
}

const ( lead1 = iota; contByte; invalidLead )

func class(b byte) int {
	switch {
	case b < 0x80:
		return lead1
	case b < 0xC2:
		if b >= 0x80 { return contByte }
		return invalidLead // C0 C1
	case b < 0xF5:
		return lead1
	default:
		return invalidLead // F5..FF
	}
}

func validCont(p []byte, b byte) bool {
	if b < 0x80 || b > 0xBF { return false }
	switch p[0] {
	case 0xE0:
		return len(p) > 1 || b >= 0xA0
	case 0xED:
		return len(p) > 1 || b <= 0x9F
	case 0xF0:
		return len(p) > 1 || b >= 0x90
	case 0xF4:
		return len(p) > 1 || b <= 0x8F
	}
	return true
}

// Feed 喂入一个字节。ok 为 false 时该字节必须作为新单元首字节重放（退 1）。
func (d *Decoder) Feed(b byte) (u Unit, ok bool) {
	if len(d.pend) == 0 {
		switch {
		case b < 0x80:
			return Unit{Rune: rune(b), Len: 1}, true
		case class(b) == invalidLead || (b >= 0x80 && b < 0xC0):
			return Unit{Rune: scalar.Replacement, Illegal: true, Len: 1}, true
		}
		d.pend = append(d.pend, b)
		return Unit{}, true
	}
	n := len(d.pend)
	if b&0xC0 != 0x80 || !validCont(d.pend, b) {
		d.pend = d.pend[:0]
		return Unit{Rune: scalar.Replacement, Illegal: true, Len: n}, false
	}
	d.pend = append(d.pend, b)
	if need(d.pend[0]) == len(d.pend) {
		r := decode(d.pend)
		buf := append([]byte(nil), d.pend...)
		d.pend = d.pend[:0]
		return Unit{Rune: r, Len: len(buf)}, true
	}
	return Unit{}, true
}

// Close 报告残留合法前缀：截断时返回一个非法/截断单元吞掉全部残留。
func (d *Decoder) Close() Unit {
	if len(d.pend) == 0 { return Unit{} }
	n := len(d.pend)
	d.pend = d.pend[:0]
	return Unit{Rune: scalar.Replacement, Illegal: true, EOF: true, Len: n}
}

func need(b byte) int {
	switch {
	case b < 0xE0: return 2
	case b < 0xF0: return 3
	default: return 4
	}
}

func decode(p []byte) rune {
	var r rune
	switch len(p) {
	case 2:
		r = rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		r = rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	default:
		r = rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 | rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
	return r
}

// Encode 把一个标量编码成 UTF-8 字节（调用方保证为标量）。
func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F, 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
}
