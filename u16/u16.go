// Package u16 提供字节级 UTF-16LE/BE 解码与编码（不使用 unicode/utf16）。
package u16

import "ontology/scalar"

const bom = 0xFEFF

// Order 是 UTF-16 字节序。
type Order uint8

const (
	LE Order = iota
	BE
)

// Event 是一个 16 位码元被处理后吐出的单元（Units 为吞掉的码元数 1 或 2）。
type Event struct {
	Rune  rune
	Bad   bool
	Units int
}

// Decoder 持有跨 Write 的高代理状态。
type Decoder struct {
	order Order
	hi    uint16
	wait  bool
}

// NewDecoder 以默认字节序创建解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o} }

// SetOrder 在 BOM 确定字节序后调用。
func (d *Decoder) SetOrder(o Order) { d.order = o }

// Unit 按字节序把两个字节拼成码元。
func Unit(o Order, b0, b1 byte) uint16 {
	if o == BE {
		return uint16(b0)<<8 | uint16(b1)
	}
	return uint16(b1)<<8 | uint16(b0)
}

// StepUnit 处理一个码元，返回 0..2 个事件：高代理挂起时无事件；
// 高代理后跟非低代理时，高代理单独成非法单元，该码元重新按新字符处理。
func (d *Decoder) StepUnit(u uint16) []Event {
	if d.wait {
		d.wait = false
		if scalar.IsLowSurrogate(u) {
			return []Event{{Rune: scalar.JoinSurrogate(d.hi, u), Units: 2}}
		}
		ev := []Event{{Rune: 0xFFFD, Bad: true, Units: 1}}
		switch {
		case scalar.IsHighSurrogate(u):
			d.hi, d.wait = u, true
		case scalar.IsLowSurrogate(u):
			ev = append(ev, Event{Rune: 0xFFFD, Bad: true, Units: 1})
		default:
			ev = append(ev, Event{Rune: rune(u), Units: 1})
		}
		return ev
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi, d.wait = u, true
		return nil
	case scalar.IsLowSurrogate(u):
		return []Event{{Rune: 0xFFFD, Bad: true, Units: 1}}
	default:
		return []Event{{Rune: rune(u), Units: 1}}
	}
}

// WaitingHigh 报告是否残留一个高代理。
func (d *Decoder) WaitingHigh() bool { return d.wait }

// FlushHigh 在流结束时处理残留高代理：一个非法单元（按截断处理）。
func (d *Decoder) FlushHigh() (Event, bool) {
	if !d.wait {
		return Event{}, false
	}
	d.wait = false
	return Event{Rune: 0xFFFD, Bad: true, Units: 1}, true
}

func encUnit(dst []byte, u uint16, o Order) []byte {
	if o == BE {
		return append(dst, byte(u>>8), byte(u))
	}
	return append(dst, byte(u), byte(u>>8))
}

// AppendBOM 追加 U+FEFF 的 BOM 表示。
func AppendBOM(dst []byte, o Order) []byte { return encUnit(dst, bom, o) }

// AppendEncode 把合法标量编码为 UTF-16（U+10000 以上编成代理对）。
func AppendEncode(dst []byte, r rune, o Order) []byte {
	if r > 0xFFFF {
		hi, lo := scalar.SplitSurrogate(r)
		dst = encUnit(dst, hi, o)
		return encUnit(dst, lo, o)
	}
	return encUnit(dst, uint16(r), o)
}
