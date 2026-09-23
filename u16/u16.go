// Package u16 实现 UTF-16LE/UTF-16BE 逐标量解码/编码，禁止使用 unicode/utf16。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order int

const (
	LE Order = iota
	BE
)

// Unit 是一次解码结果。
type Unit struct {
	R     scalar.Rune
	Valid bool
	Off   int // 单元起始字节偏移（绝对）
	Len   int // 吞掉字节数：孤立/普通=2，代理对=4
}

// Decoder 是有状态的 UTF-16 解码器。
type Decoder struct {
	ord     Order
	lo      byte // 已缓存的奇数字节
	loOff   int
	have    bool
	hi      scalar.Rune
	hiOn    bool
	hiOff   int
	started bool // 首单元 BOM 是否已判定
}

func NewDecoder(o Order) *Decoder { return &Decoder{ord: o} }

func (d *Decoder) u16(a, b byte) scalar.Rune {
	if d.ord == LE {
		return scalar.Rune(a) | scalar.Rune(b)<<8
	}
	return scalar.Rune(a)<<8 | scalar.Rune(b)
}

func (d *Decoder) consume(w scalar.Rune, off int, emit func(Unit)) {
	if !d.started {
		d.started = true
		if w == scalar.BOM {
			return
		}
	}
	switch {
	case d.hiOn:
		if scalar.IsLowSurrogate(w) {
			r, _ := scalar.DecodeSurrogatePair(d.hi, w)
			emit(Unit{R: r, Valid: true, Off: d.hiOff, Len: 4})
			d.hiOn = false
		} else {
			emit(Unit{R: scalar.Replacement, Off: d.hiOff, Len: 2})
			d.hiOn = false
			d.consume(w, off, emit)
		}
	case scalar.IsHighSurrogate(w):
		d.hi, d.hiOn, d.hiOff = w, true, off
	default:
		if scalar.IsLowSurrogate(w) {
			emit(Unit{R: scalar.Replacement, Off: off, Len: 2})
		} else {
			emit(Unit{R: w, Valid: true, Off: off, Len: 2})
		}
	}
}

// Feed 喂入字节，base 为 p[0] 绝对偏移；每字节检查一次。
func (d *Decoder) Feed(p []byte, base int, emit func(Unit)) {
	i := 0
	if d.have { // 与缓存字节配对
		var a byte
		a, i = d.lo, 1
		d.have = false
		d.consume(d.u16(a, p[0]), base-1, emit)
	}
	for ; i+1 < len(p); i += 2 {
		d.consume(d.u16(p[i], p[i+1]), base+i, emit)
	}
	if i < len(p) {
		d.lo, d.loOff, d.have = p[i], base+i, true
	}
}

// Flush 报告残留：返回截断类型（奇数字节 / 孤立高代理）。
func (d *Decoder) Flush() (off, n int, kind int) {
	kind, off, n = -1, 0, 0
	if d.have {
		off, n, kind = d.loOff, 1, 0
		d.have = false
	}
	if d.hiOn {
		off, n, kind = d.hiOff, 2, 1
		d.hiOn = false
	}
	return
}

// Encode 把合法标量追加编码为指定字节序。
func Encode(dst []byte, r scalar.Rune, o Order) []byte {
	if hi, lo, ok := scalar.EncodeSurrogatePair(r); ok {
		dst = put2(dst, hi, o)
		dst = put2(dst, lo, o)
		return dst
	}
	return put2(dst, r, o)
}

func put2(dst []byte, w scalar.Rune, o Order) []byte {
	if o == LE {
		return append(dst, byte(w), byte(w>>8))
	}
	return append(dst, byte(w>>8), byte(w))
}

// EncLen 返回 r 的 UTF-16 编码字节数（2 或 4）。
func EncLen(r scalar.Rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}
