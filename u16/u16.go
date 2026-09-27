package u16

import "ontology/scalar"

// Order 标记字节序。
type Order int

const (
	LE Order = iota
	BE
)

// Unit 是一次解码结果：Len 为吞掉的输入字节数（2 或 4；孤立代理为 2）。
type Unit struct {
	R   scalar.Rune
	Len int
}

// Decoder 是逐字节增量 UTF-16 解码器，字节序在构造时固定。
type Decoder struct {
	order   Order
	half    bool
	b       byte
	high    rune
	hasHigh bool
	queued  Unit
	hasQ    bool
	Checked int64
}

func NewDecoder(o Order) *Decoder { return &Decoder{order: o} }

// Step 喂入一个字节。consumed=false 时 b 未被消费（取出了一个排队单元），
// 调用方必须用同一个 b 再调一次。
func (d *Decoder) Step(b byte) (u Unit, consumed bool) {
	if d.hasQ {
		d.hasQ = false
		return d.queued, false
	}
	d.Checked++
	if !d.half {
		d.b, d.half = b, true
		return Unit{}, true
	}
	d.half = false
	var code rune
	if d.order == LE {
		code = rune(b)<<8 | rune(d.b)
	} else {
		code = rune(d.b)<<8 | rune(b)
	}
		return d.unitFrom(code), true
	}

func (d *Decoder) unitFrom(code rune) Unit {
	switch {
	case d.hasHigh:
		d.hasHigh = false
		if scalar.LowSurrogate(code) {
			return Unit{R: scalar.Rune{Value: scalar.FromSurrogatePair(d.high, code), Valid: true}, Len: 4}
		}
		// 高代理后不是低代理：高代理单独为非法单元（只吞 2 字节）；
		// code 排队，作为新代码单元重新处理。
		d.queued, d.hasQ = d.unitFrom(code), true
		return Unit{R: scalar.Rune{Valid: false}, Len: 2}
	case scalar.HighSurrogate(code):
		d.hasHigh, d.high = true, code
		return Unit{}
	case scalar.LowSurrogate(code):
		return Unit{R: scalar.Rune{Valid: false}, Len: 2}
	default:
		return Unit{R: scalar.Rune{Value: code, Valid: scalar.Scalar(code)}, Len: 2}
	}
}

// EOF 标记流结束：奇数字节或残留高代理都是截断非法单元（Len=1 或 2）。
func (d *Decoder) EOF() Unit {
	if d.hasQ {
		d.hasQ = false
		if d.queued.Len > 0 {
			return d.queued
		}
		return Unit{R: scalar.Rune{Valid: false}, Len: 2}
	}
	switch {
	case d.half:
		d.half = false
		return Unit{R: scalar.Rune{Valid: false}, Len: 1}
	case d.hasHigh:
		d.hasHigh = false
		return Unit{R: scalar.Rune{Valid: false}, Len: 2}
	default:
		return Unit{}
	}
}

// Pending 返回缓存占用字节数（上限 3：半个单元 + 高代理）。
func (d *Decoder) Pending() int {
	n := 0
	if d.half {
		n++
	}
	if d.hasHigh {
		n += 2
	}
	return n
}

// EncodeLen 返回标量编码后的字节数（>FFFF 为代理对 4 字节）。
func EncodeLen(r rune) int {
	if !scalar.Scalar(r) {
		return 0
	}
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// Encode 按 order 编码标量。
func Encode(r rune, order Order) []byte {
	units := []rune{r}
	if r >= 0x10000 {
		hi, lo := scalar.ToSurrogatePair(r)
		units = []rune{hi, lo}
	}
	out := make([]byte, 0, 4)
	for _, u := range units {
		if order == LE {
			out = append(out, byte(u), byte(u>>8))
		} else {
			out = append(out, byte(u>>8), byte(u))
		}
	}
	return out
}

// AlignStart 返回 UTF-16 切点 cut 应对齐到的偶数边界（回看 ≤1 字节）。
func AlignStart(cut int) int {
	if cut%2 == 0 {
		return cut
	}
	return cut - 1
}
