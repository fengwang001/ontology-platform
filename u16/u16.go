// Package u16 手写 UTF-16LE/BE 增量解码与编码（不用 unicode/utf16）。
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

const (
	// MaxPending 是 UTF-16 切分缓存硬上限：一个奇数字节，或一个高代理码元。
	MaxPending = 2
	// RuneError 非法/截断单元占位。
	RuneError = scalar.Replacement
)

// Decoder 是不回溯的增量 UTF-16 解码器。
type Decoder struct {
	order    Order
	hi       rune  // 待配对的高代理；-1 表示无
	odd      byte  // 残留奇数字节
	hasOdd   bool
	checks   int64
	consumed int64
}

// NewDecoder 创建指定字节序解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o, hi: -1} }

// Checks 返回字节被检查总次数（每字节至多两次：组码元、配代理）。
func (d *Decoder) Checks() int64 { return d.checks }

// Pending 返回缓存字节数（0..MaxPending）。
func (d *Decoder) Pending() int {
	n := 0
	if d.hasOdd {
		n++
	}
	if d.hi >= 0 {
		n += 2
	}
	return n
}

// Consumed 返回已消费绝对字节数。
func (d *Decoder) Consumed() int64 { return d.consumed }

// Feed 喂入字节；每形成一个单元回调 fn(r,n)，孤立代理 r==RuneError n==2；
// 高代理后跟非低代理时，非低代理码元被释放重解析（n==2 仅计高代理）。
func (d *Decoder) Feed(p []byte, fn func(r rune, n int)) int {
	i := 0
	for i < len(p) {
		var cu rune
		if d.hasOdd {
			d.checks++
			if d.order == LE {
				cu = rune(d.odd) | rune(p[i])<<8
			} else {
				cu = rune(d.odd)<<8 | rune(p[i])
			}
			d.hasOdd = false
			i++
			d.consumed++
		} else {
			if i+1 >= len(p) {
				d.checks++
				d.odd, d.hasOdd = p[i], true
				i++
				d.consumed++
				break
			}
			d.checks += 2
			if d.order == LE {
				cu = rune(p[i]) | rune(p[i+1])<<8
			} else {
				cu = rune(p[i])<<8 | rune(p[i+1])
			}
			i += 2
			d.consumed += 2
		}
		d.emit(cu, fn)
	}
	return i
}

func (d *Decoder) emit(cu rune, fn func(r rune, n int)) {
	switch {
	case d.hi >= 0:
		if scalar.IsLowSurrogate(cu) {
			fn(scalar.FromSurrogatePair(d.hi, cu), 4)
			d.hi = -1
		} else {
			fn(RuneError, 2) // 孤立高代理；cu 释放重解析
			d.hi = -1
			d.emit(cu, fn)
		}
	case scalar.IsHighSurrogate(cu):
		d.hi = cu
	case scalar.IsLowSurrogate(cu):
		fn(RuneError, 2) // 孤立低代理
	default:
		fn(cu, 2)
	}
}

// End 标记流结束：残留奇数字节或高代理均为截断，输出一个截断单元。
func (d *Decoder) End(fn func(r rune, n int)) bool {
	trunc := false
	if d.hasOdd {
		fn(RuneError, 1)
		d.hasOdd, trunc = false, true
	}
	if d.hi >= 0 {
		fn(RuneError, 2)
		d.hi, trunc = -1, true
	}
	return trunc
}

// Encode 把 r 按字节序追加到 dst；r>=10000 编成代理对，代理码点按替换符。
func Encode(dst []byte, r rune, o Order) []byte {
	if scalar.IsSurrogate(r) || r > scalar.MaxRune {
		return Encode(dst, RuneError, o)
	}
	if r < 0x10000 {
		return appendUnit(dst, uint16(r), o)
	}
	hi, lo := scalar.SurrogatePair(r)
	dst = appendUnit(dst, uint16(hi), o)
	return appendUnit(dst, uint16(lo), o)
}

// EncodeLen 返回 r 的 UTF-16 字节长度。
func EncodeLen(r rune) int {
	if scalar.IsSurrogate(r) || r > scalar.MaxRune {
		return 2
	}
	if r < 0x10000 {
		return 2
	}
	return 4
}

func appendUnit(dst []byte, u uint16, o Order) []byte {
	if o == LE {
		return append(dst, byte(u), byte(u>>8))
	}
	return append(dst, byte(u>>8), byte(u))
}
