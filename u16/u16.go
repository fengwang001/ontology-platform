// Package u16 提供字节级 UTF-16LE/BE 解码与编码。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order int

const (
	// LE 小端，BE 大端。
	LE Order = iota
	BE
)

// Kind 是事件类别，含义同 u8。
type Kind int

const (
	None Kind = iota
	Need
	Ready
	Bad
	Incomplete
)

// Event 是 Push 结果；Ready 时 Rune 有效。
type Event struct {
	Kind   Kind
	Rune   scalar.Rune
	Length int // 该单元吞掉的输入字节数
}

// Decoder 是增量 UTF-16 字节状态机。
type Decoder struct {
	order              Order
	first              bool
	hi                 bool
	hival              uint16
	lo                 byte
	haveLo             bool
	queued             Event
	haveQueued         bool
}

// NewDecoder 创建解码器；开头 BOM 会自动确定字节序。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o, first: true} }

// Push 喂入一个字节。
func (d *Decoder) Push(b byte) Event {
	if d.haveQueued {
		e := d.queued
		d.queued = Event{}
		d.lo, d.haveLo = b, true
		return e
	}
	if !d.haveLo {
		d.lo, d.haveLo = b, true
		return Event{Kind: Need}
	}
	u := d.pair(d.lo, b)
	d.haveLo = false
	return d.unit(u)
}

func (d *Decoder) pair(lo, hi byte) uint16 {
	if d.order == LE {
		return uint16(lo) | uint16(hi)<<8
	}
	return uint16(hi) | uint16(lo)<<8
}

func (d *Decoder) unit(u uint16) Event {
	if d.first {
		d.first = false
		if u == 0xFEFF {
			return Event{Ready, 0xFEFF, 2}
		}
		if u == 0xFFFE {
			d.order = 1 - d.order
			return Event{Ready, 0xFEFF, 2}
		}
	}
	switch {
	case scalar.HighSurrogate(u):
		d.hi, d.hival = true, u
		return Event{Kind: Need}
	case d.hi:
		d.hi = false
		if scalar.LowSurrogate(u) {
			return Event{Ready, scalar.Pair(d.hival, u), 4}
		}
		d.queued, d.haveQueued = d.unit(u), true
		return Event{Bad, 0xFFFD, 2}
	case scalar.LowSurrogate(u):
		return Event{Bad, 0xFFFD, 2}
	default:
		return Event{Ready, scalar.Rune(u), 2}
	}
}

// Flush 报告奇数残留或孤立高代理。
func (d *Decoder) Flush() Event {
	switch {
	case d.haveQueued:
		e := d.queued
		d.haveQueued = false
		return e
	case d.haveLo:
		d.haveLo = false
		return Event{Incomplete, 0xFFFD, 1}
	case d.hi:
		d.hi = false
		return Event{Incomplete, 0xFFFD, 2}
	default:
		return Event{Kind: None}
	}
}

// Reset 回到初始状态。
func (d *Decoder) Reset() { *d = Decoder{order: d.order, first: true} }

// Order 返回当前字节序（BOM 可能已改变它）。
func (d *Decoder) Order() Order { return d.order }

// Encode 把标量编码为指定字节序的 UTF-16 字节（r=0xFEFF 即 BOM）。
func Encode(r scalar.Rune, o Order) []byte {
	put := func(u uint16) []byte {
		if o == LE {
			return []byte{byte(u), byte(u >> 8)}
		}
		return []byte{byte(u >> 8), byte(u)}
	}
	if r < 0x10000 {
		return put(uint16(r))
	}
	r -= 0x10000
	hi, lo := uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF))
	return append(put(hi), put(lo)...)
}

// RuneLen 返回 UTF-16 编码占用的码元数（1 或 2）。
func RuneLen(r scalar.Rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}
