// Package u16 在字节级实现 UTF-16LE/BE 解码与编码。
package u16

import "ontology/scalar"

// Order 选择字节序。
type Order uint8

const (
	LE Order = iota
	BE
)

// BOM 代码单元 U+FEFF。
const BOM = 0xFEFF

// Event 是推进字节后的事件。
type Event uint8

const (
	NeedMore Event = iota
	Scalar
	Invalid
)

// Result 描述 Push 结果。
type Result struct {
	Event     Event
	R         rune
	Consumed  int
	Reprocess bool
}

// Decoder 是增量 UTF-16 字节状态机，单实例非并发安全。
type Decoder struct {
	order Order
	pend  [2]byte
	n     int
	hi    uint16
	hasHi bool
}

// NewDecoder 按指定字节序构造。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o} }

// Pending 返回尚未配对成代码单元的缓存字节数（0..1）。
func (d *Decoder) Pending() int { return d.n }

// PendingBytes 复制缓存字节。
func (d *Decoder) PendingBytes(b []byte) int { return copy(b, d.pend[:d.n]) }

// Push 推进一个字节，每个字节只检查一次。
func (d *Decoder) Push(b byte) Result {
	d.pend[d.n] = b
	d.n++
	if d.n < 2 {
		return Result{Event: NeedMore}
	}
	d.n = 0
	u := d.unit(d.pend[0], d.pend[1])
	return d.unitEvent(u)
}

func (d *Decoder) unitEvent(u uint16) Result {
	switch {
	case scalar.IsHighSurrogate(u):
		if d.hasHi {
			d.hi = u
			return Result{Event: Invalid, Consumed: 2, Reprocess: true}
		}
		d.hasHi = true
		d.hi = u
		return Result{Event: NeedMore}
	case scalar.IsLowSurrogate(u):
		if !d.hasHi {
			return Result{Event: Invalid, Consumed: 2}
		}
		r := scalar.JoinSurrogates(d.hi, u)
		d.hasHi = false
		return Result{Event: Scalar, R: r, Consumed: 4}
	default:
		if d.hasHi {
			d.hasHi = false
			return Result{Event: Invalid, Consumed: 2, Reprocess: true}
		}
		return Result{Event: Scalar, R: rune(u), Consumed: 2}
	}
}

func (d *Decoder) unit(lo, hi byte) uint16 {
	if d.order == LE {
		return uint16(lo) | uint16(hi)<<8
	}
	return uint16(hi) | uint16(lo)<<8
}

// Flush 在流结束调用：ok=false 时 badUnits 为残留造成的非法单元数。
func (d *Decoder) Flush() (consumed int, badUnits int, ok bool) {
	if d.hasHi {
		d.hasHi = false
		if d.n == 1 {
			d.n = 0
			return 3, 1, false
		}
		return 2, 1, false
	}
	if d.n == 1 {
		d.n = 0
		return 1, 1, false
	}
	return 0, 0, true
}

// Reset 清空状态。
func (d *Decoder) Reset() { d.n, d.hasHi = 0, false }

// Encode 把标量编码成指定字节序的 UTF-16 字节。
func Encode(r rune, o Order) []byte {
	var units []uint16
	if scalar.NeedsSurrogates(r) {
		h, l := scalar.SplitSurrogates(r)
		units = []uint16{h, l}
	} else {
		units = []uint16{uint16(r)}
	}
	out := make([]byte, 2*len(units))
	for i, u := range units {
		if o == LE {
			out[2*i] = byte(u)
			out[2*i+1] = byte(u >> 8)
		} else {
			out[2*i] = byte(u >> 8)
			out[2*i+1] = byte(u)
		}
	}
	return out
}

// FirstUnit 返回 BOM 的两个原始字节，用于流首探测字节序。
func FirstUnit(b0, b1 byte) uint16 { return uint16(b0) | uint16(b1)<<8 }
