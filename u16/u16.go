// Package u16 手写字节级 UTF-16LE/BE 解码/编码，不使用 unicode/utf16。
package u16

import "ontology/scalar"

const MaxPending = 3 // 半个码元 1 字节，或高代理 2 字节

// Order 指定字节序；Detect 表示仅由流首 BOM 决定（无 BOM 时按 BE）。
type Order int

const (
	Detect Order = iota
	LittleEndian
	BigEndian
)

// Decoder 是逐字节 UTF-16 状态机。
type Decoder struct {
	order                       Order
	little, detected, firstDone bool
	haveByte                    bool
	lo                          byte
	haveHigh                    bool
	high                        uint16
	rep                         [2]byte
	repN                        int
	Checks                      int64
}

func NewDecoder(order Order) *Decoder {
	d := &Decoder{order: order}
	d.little = order == LittleEndian
	return d
}

func (d *Decoder) Reset() {
	o := d.order
	*d = Decoder{order: o}
	d.little = o == LittleEndian
}

// Pending 返回缓存中尚未成单元的字节数（奇数字节=1，待低代理=2）。
func (d *Decoder) Pending() int {
	if d.haveByte {
		return 1
	}
	if d.haveHigh {
		return 2
	}
	return 0
}

// Push 喂入一个字节。bom 为真表示 size 字节是流首 BOM（仅用于定序/记账，无标量）。
func (d *Decoder) Push(b byte) (size int, r rune, invalid, reprocess, bom bool) {
	d.Checks++
	if d.repN > 0 { // 上一事件要求重新解析的完整码元
		x0, x1 := d.rep[0], d.rep[1]
		d.repN = 0
		d.haveByte, d.lo = true, x0
		return d.unit(x1)
	}
	if !d.haveByte {
		d.haveByte, d.lo = true, b
		return 0, 0, false, false, false
	}
	d.haveByte = false
	return d.unit(b)
}

func (d *Decoder) unit(hiByte byte) (size int, r rune, invalid, reprocess, bom bool) {
	u := uint16(hiByte)<<8 | uint16(d.lo)
	if d.little {
		u = uint16(d.lo)<<8 | uint16(hiByte)
	}
	if d.order == Detect && !d.firstDone {
		d.firstDone = true
		if u == 0xFEFF {
			d.detected, d.little = true, false
			return 2, 0, false, false, true
		}
		if u == 0xFFFE {
			d.detected, d.little = true, true
			return 2, 0, false, false, true
		}
		d.little = false // 无 BOM：默认 BE
	}
	if d.haveHigh {
		d.haveHigh = false
		if scalar.IsLowSurrogate(u) {
			return 4, scalar.SurrogatePair(d.high, u), false, false, false
		}
		// 高代理孤立：高代理自成非法单元，当前码元整体重新解析
		d.rep, d.repN = [2]byte{d.lo, hiByte}, 2
		if d.little {
			d.rep = [2]byte{hiByte, d.lo}
		}
		return 2, 0, true, true, false
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.haveHigh, d.high = true, u
		return 0, 0, false, false, false
	case scalar.IsLowSurrogate(u):
		return 2, 0, true, false, false // 孤立低代理
	default:
		return 2, rune(u), false, false, false
	}
}

// Finish 在流结束调用：残留奇数字节或高代理均报告截断（truncated=true）。
func (d *Decoder) Finish() (size int, invalid, truncated bool) {
	switch {
	case d.haveByte:
		d.haveByte = false
		return 1, false, true
	case d.haveHigh:
		d.haveHigh = false
		return 2, false, true
	default:
		return 0, false, false
	}
}

// Little reports the byte order resolved after BOM detection.
func (d *Decoder) Little() bool { return d.little }

// Encode 把标量按指定字节序追加到 dst。
func Encode(dst []byte, r rune, little bool) []byte {
	appendUnit := func(u uint16) []byte {
		if little {
			return append(dst, byte(u), byte(u>>8))
		}
		return append(dst, byte(u>>8), byte(u))
	}
	if r >= 0x10000 {
		hi, lo := scalar.EncodeSurrogates(r)
		dst = appendUnit(hi)
		dst = appendUnit(lo)
		return dst
	}
	return appendUnit(uint16(r))
}
