// Package u16 在字节级实现 UTF-16LE/BE 的逐标量解码与编码。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order int

const (
	LE Order = iota
	BE
)

// Replacement 是替换字符 U+FFFD。
const Replacement rune = 0xFFFD

// PendingCap 是待解析字节硬上限（半 code unit + 高代理 = 3）。
const PendingCap = 3

// Event 是每消费一个单元后的事件。
type Event struct {
	R    rune
	OK   bool
	Len  int // 吞掉的输入字节数（2 或 4）
	From int // 其中来自本次喂入的字节数
}

// Decoder 是有状态 UTF-16 解码器。
type Decoder struct {
	order Order
	half  byte // 奇数字节残留
	nhalf bool
	hi    rune // 待配对的高代理；0 表示无
	hasHi bool
}

// NewDecoder 构造解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o} }

// SetOrder 设置字节序（仅流开头 BOM 探测使用一次）。
func (d *Decoder) SetOrder(o Order) { d.order = o }

func (d *Decoder) unit(a, b byte) rune {
	if d.order == LE {
		return rune(a) | rune(b)<<8
	}
	return rune(b) | rune(a)<<8
}

// Feed 喂入数据。返回事件数与未消费尾部；From 仅计本次喂入字节数。
func (d *Decoder) Feed(p []byte, evs []Event, checks *int64) (k int, rest []byte) {
	rest = p
	for len(rest) > 0 && k+2 <= len(evs) {
		var u rune
		var from int
		if d.nhalf {
			u = d.unit(d.half, rest[0])
			*checks += 2
			rest = rest[1:]
			d.nhalf = false
			from = 1
		} else if len(rest) < 2 {
			d.half = rest[0]
			d.nhalf = true
			*checks++
			return k, nil
		} else {
			u = d.unit(rest[0], rest[1])
			*checks += 2
			rest = rest[2:]
			from = 2
		}
		if d.hasHi {
			if scalar.IsLowSurrogate(u) {
				r, _ := scalar.FromSurrogates(d.hi, u)
				d.hasHi = false
				evs[k] = Event{R: r, OK: true, Len: 4, From: from}
				k++
				continue
			}
			d.hasHi = false
			evs[k] = Event{R: Replacement, Len: 2, From: 0} // 孤立高代理
			k++
			// u 作为新 code unit 重新处理，其 from 不变。
		}
		switch {
		case scalar.IsScalar(u):
			evs[k] = Event{R: u, OK: true, Len: 2, From: from}
			k++
		case scalar.IsLowSurrogate(u):
			evs[k] = Event{R: Replacement, Len: 2, From: from}
			k++
		default:
			d.hi, d.hasHi = u, true
		}
	}
	return k, rest
}

// PendingLen 返回残留字节数（半 code unit=1，待配高代理=2）。
func (d *Decoder) PendingLen() int {
	if d.nhalf {
		return 1
	}
	if d.hasHi {
		return 2
	}
	return 0
}

// Flush 报告流结束残留：半 code unit 或未配对高代理。
func (d *Decoder) Flush() *Event {
	if d.nhalf {
		d.nhalf = false
		return &Event{R: Replacement, Len: 1, From: 1}
	}
	if d.hasHi {
		d.hasHi = false
		return &Event{R: Replacement, Len: 2, From: 2}
	}
	return nil
}

// Encode 把标量编码到 UTF-16 字节序 o；非 scalar 返回 false。
func Encode(r rune, o Order) ([]byte, bool) {
	if !scalar.IsScalar(r) {
		return nil, false
	}
	put := func(u uint16) []byte {
		if o == LE {
			return []byte{byte(u), byte(u >> 8)}
		}
		return []byte{byte(u >> 8), byte(u)}
	}
	if r < 0x10000 {
		return put(uint16(r)), true
	}
	r -= 0x10000
	hi := uint16(0xD800 + r>>10)
	lo := uint16(0xDC00 + r&0x3FF)
	return append(put(hi), put(lo)...), true
}
