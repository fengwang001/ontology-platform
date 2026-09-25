// Package u16 实现 UTF-16LE/UTF-16BE 的增量解码与编码。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order uint8

const (
	// LE 是小端，BE 是大端；Auto 表示「无 BOM 时用默认序」。
	LE Order = iota
	BE
)

// Unit 是一次 UTF-16 解码判定。
type Unit struct {
	R        rune
	OK       bool
	Consumed int  // 吞掉的输入字节数 2 或 4
	Incomp   bool // 奇数残留字节或残留高代理
	IsBOM    bool // 流开头的 U+FEFF（BOM）
}

// Dec 是增量 UTF-16 解码器。
type Dec struct {
	def     Order // 无 BOM 时的默认序
	bo      Order
	boKnown bool
	bomSeen bool
	hi      rune // 待配对的高代理；<0 表示无
	odd     byte
	hasOdd  bool
	chk     int
}

// NewDec 创建解码器；def 为没有起始 BOM 时采用的字节序。
func NewDec(def Order) *Dec { return &Dec{def: def, bo: def, hi: -1} }

// Checks 返回字节被检查总次数。
func (d *Dec) Checks() int { return d.chk }

// Order 返回当前生效字节序（BOM 定序后可能改变）。
func (d *Dec) Order() Order { return d.bo }

func (d *Dec) unit(cu rune, n int) []Unit {
	if !d.bomSeen {
		d.bomSeen = true
		if cu == 0xFEFF {
			return []Unit{{R: cu, OK: true, Consumed: n, IsBOM: true}}
		}
	}
	if scalar.IsHighSurrogate(cu) {
		d.hi = cu
		return nil
	}
	if scalar.IsLowSurrogate(cu) {
		return []Unit{{R: scalar.Replacement, Consumed: n}}
	}
	return []Unit{{R: cu, OK: true, Consumed: n}}
}

// Step 喂入一个 code unit（由上层成对组装字节后调用），n 为其字节数。
func (d *Dec) Step(cu rune, n int) []Unit {
	if d.hi >= 0 {
		hi := d.hi
		d.hi = -1
		if scalar.IsLowSurrogate(cu) {
			r, _ := scalar.SurrogatePair(hi, cu)
			return []Unit{{R: r, OK: true, Consumed: n + 2}}
		}
		out := []Unit{{R: scalar.Replacement, Consumed: 2}}
		return append(out, d.unit(cu, n)...)
	}
	return d.unit(cu, n)
}

// Byte 喂入单个原始字节，按字节序成对。
func (d *Dec) Byte(b byte) []Unit {
	if !d.hasOdd {
		d.odd, d.hasOdd = b, true
		return nil
	}
	d.hasOdd = false
	le, be := rune(b)<<8|rune(d.odd), rune(d.odd)<<8|rune(b)
	if !d.boKnown {
		d.boKnown = true
		switch {
		case le == 0xFEFF:
			d.bo = LE
		case be == 0xFEFF:
			d.bo = BE
		default:
			d.bo = d.def
		}
	}
	cu := le
	if d.bo == BE {
		cu = be
	}
	d.chk += 2
	return d.Step(cu, 2)
}

// Flush 处理流尾：奇数残留字节或未配对高代理都是截断。
func (d *Dec) Flush() (Unit, bool) {
	if d.hasOdd {
		d.hasOdd = false
		d.chk++
		return Unit{R: scalar.Replacement, Consumed: 1, Incomp: true}, true
	}
	if d.hi >= 0 {
		d.hi = -1
		d.chk += 2
		return Unit{R: scalar.Replacement, Consumed: 2, Incomp: true}, true
	}
	return Unit{}, false
}

// EncodeAppend 把标量按 ord 编码为 UTF-16 追加到 out。
func EncodeAppend(out []byte, r rune, ord Order) []byte {
	var cus [2]rune
	n := 1
	if hi, lo, ok := scalar.SplitSurrogate(r); ok {
		cus[0], cus[1], n = hi, lo, 2
	} else {
		cus[0] = r
	}
	for i := 0; i < n; i++ {
		hiB, loB := byte(cus[i]>>8), byte(cus[i])
		if ord == LE {
			out = append(out, loB, hiB)
		} else {
			out = append(out, hiB, loB)
		}
	}
	return out
}
