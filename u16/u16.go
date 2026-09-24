// Package u16 是手写的 UTF-16LE/UTF-16BE 字节级解码器与编码器。
package u16

import "ontology/scalar"

const (
	OK  = iota // 合法标量（BMP 或代理对）
	Bad        // 非法单元：孤立高/低代理（Len 为字节数）
)

// Event 是解码器推进的结果。
type Event struct {
	Kind  int
	R     rune
	Len   int // 字节数：BMP 为 2，代理对为 4，孤立代理为 2
	Start int64
}

// Decoder 逐字节推进 UTF-16 解码。big 指定字节序。
type Decoder struct {
	big    bool
	lo     bool // 已缓存奇数位置的一个字节
	b0     byte
	hi     uint16 // 非 0 表示挂起一个高代理
	hiOff  int64
	seen   int64
	queued bool // 高代理非法后，同一码元重解析出的事件排队
	q      Event
}

// NewDecoder 构造解码器。
func NewDecoder(bigEndian bool) *Decoder { return &Decoder{big: bigEndian} }

// Reset 清空状态机（保留字节序）。
func (d *Decoder) Reset() {
	b := d.big
	*d = Decoder{big: b}
}

// Pending 返回挂起字节数：奇数字节 1，或挂起高代理时额外 2。
func (d *Decoder) Pending() int {
	n := 0
	if d.lo {
		n++
	}
	if d.hi != 0 {
		n += 2
	}
	return n
}

func (d *Decoder) unit(b0, b1 byte) uint16 {
	if d.big {
		return uint16(b0)<<8 | uint16(b1)
	}
	return uint16(b1)<<8 | uint16(b0)
}

// Feed 喂入一个字节；凑满码元或排队事件待取时返回事件。
func (d *Decoder) Feed(b byte) (Event, bool) {
	d.seen++
	if d.lo {
		d.lo = false
		return d.onUnit(d.unit(d.b0, b), d.seen-2)
	}
	d.lo, d.b0 = true, b
	return Event{}, false
}

func (d *Decoder) onUnit(u uint16, off int64) (Event, bool) {
	if d.hi != 0 {
		ho := d.hiOff
		if scalar.IsLowSurrogate(u) {
			r := scalar.SurrogatePair(d.hi, u)
			d.hi = 0
			return Event{Kind: OK, R: r, Len: 4, Start: ho}, true
		}
		// 高代理后跟非低代理：高代理单独非法；u 重新当作新码元处理。
		d.hi = 0
		e, ok := d.onUnit(u, off)
		if ok {
			d.queued, d.q = true, e
		}
		return Event{Kind: Bad, R: scalar.RuneError, Len: 2, Start: ho}, true
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi, d.hiOff = u, off
		return Event{}, false
	case scalar.IsLowSurrogate(u):
		return Event{Kind: Bad, R: scalar.RuneError, Len: 2, Start: off}, true
	default:
		return Event{Kind: OK, R: rune(u), Len: 2, Start: off}, true
	}
}

// Drain 取出排队事件（高代理非法后同码元重解析的结果）。
func (d *Decoder) Drain() (Event, bool) {
	if d.queued {
		d.queued = false
		return d.q, true
	}
	return Event{}, false
}

// Finish 报告流结束残留：kind 1 奇数字节截断；2 孤立高代理非法。
func (d *Decoder) Finish() (e Event, kind int) {
	if d.lo {
		return Event{Kind: Bad, R: scalar.RuneError, Len: 1, Start: d.seen - 1}, 1
	}
	if d.hi != 0 {
		return Event{Kind: Bad, R: scalar.RuneError, Len: 2, Start: d.hiOff}, 2
	}
	return Event{}, 0
}
