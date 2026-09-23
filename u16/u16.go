// Package u16 实现字节级 UTF-16LE/UTF-16BE 的逐标量解码与编码。
package u16

import "ontology/scalar"

const (
	// LE 表示小端字节序。
	LE = iota
	// BE 表示大端字节序。
	BE
)

// Event 是一次解码结果。OK 为真时 R 合法；否则为一个非法 UTF-16 单元（孤立代理）。
type Event struct {
	R     rune
	OK    bool
	Start int // 起始字节偏移
	Len   int // 吞掉的字节数（孤立代理=2）
}

// Decoder 是单遍 UTF-16 解码器。非并发安全。
type Decoder struct {
	order   int
	lo      byte
	hasLo   bool
	loStart int
	hi      uint16
	hasHi   bool
	hiStart int
	rf      []rfByte
	checks  int64
}

type rfByte struct {
	b   byte
	rel int
}

// NewDecoder 以指定字节序创建解码器。
func NewDecoder(order int) *Decoder { return &Decoder{order: order} }

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Pending 返回缓存字节数（0..3）。
func (d *Decoder) Pending() int {
	n := 0
	if d.hasLo {
		n++
	}
	if d.hasHi {
		n += 2
	}
	return n
}

// Refeed 取出需要作为新字符重新处理的字节（至多 2 个：一个完整码元）。
// rel 为相对触发字节偏移：第一字节在其前 1 位(-1)，第二字节即触发字节(0)。
func (d *Decoder) Refeed() (byte, int, bool) {
	if len(d.rf) == 0 {
		return 0, 0, false
	}
	b := d.rf[0]
	d.rf = d.rf[1:]
	return b.b, b.rel, true
}

// Push 喂入一个字节（偏移 off）。
func (d *Decoder) Push(b byte, off int) (e Event, refeed bool) {
	d.checks++
	if !d.hasLo {
		d.lo, d.hasLo, d.loStart = b, true, off
		return Event{}, false
	}
	u := unit(d.lo, b, d.order)
	d.hasLo = false
	start := d.loStart
	if d.hasHi {
		d.hasHi = false
		if scalar.IsLowSurrogate(rune(u)) {
			return Event{R: scalar.SurrogatePair(d.hi, u), OK: true, Start: d.hiStart, Len: 4}, false
		}
		// 高代理后非低代理：只吞高代理 2 字节，当前码元 2 字节整体重处理。
		d.rf = []rfByte{{d.lo, -1}, {b, 0}}
		return Event{Start: d.hiStart, Len: 2}, true
	}
	switch {
	case scalar.IsHighSurrogate(rune(u)):
		d.hi, d.hasHi, d.hiStart = u, true, start
		return Event{}, false
	case scalar.IsLowSurrogate(rune(u)):
		return Event{Start: start, Len: 2}, false
	default:
		return Event{R: rune(u), OK: true, Start: start, Len: 2}, false
	}
}

func unit(lo, hi byte, order int) uint16 {
	if order == LE {
		return uint16(hi)<<8 | uint16(lo)
	}
	return uint16(lo)<<8 | uint16(hi)
}

// Flush 在流结束时调用。任何残留（奇数尾字节、孤立高代理）均为“输入被截断”。
func (d *Decoder) Flush() (e Event, truncated bool) {
	switch {
	case d.hasHi:
		d.hasHi = false
		start := d.hiStart
		n := 2
		if d.hasLo {
			n = 3
		}
		d.hasLo = false
		return Event{Start: start, Len: n}, true
	case d.hasLo:
		d.hasLo = false
		return Event{Start: d.loStart, Len: 1}, true
	default:
		return Event{}, false
	}
}

// ValidPrefix 报告偶数长度前缀 p 是否可作为对齐回退点（UTF-16 无歧义前缀）。
func ValidPrefix(p []byte) bool { return len(p)%2 == 0 }

// Encode 把合法标量编码为 UTF-16 字节。
func Encode(r rune, order int) []byte {
	var us []uint16
	if r >= 0x10000 {
		hi, lo := scalar.SplitSurrogate(r)
		us = []uint16{hi, lo}
	} else {
		us = []uint16{uint16(r)}
	}
	out := make([]byte, 0, 2*len(us))
	for _, u := range us {
		if order == LE {
			out = append(out, byte(u), byte(u>>8))
		} else {
			out = append(out, byte(u>>8), byte(u))
		}
	}
	return out
}

// BOM 返回指定字节序的 BOM 字节。
func BOM(order int) []byte {
	if order == LE {
		return []byte{0xFF, 0xFE}
	}
	return []byte{0xFE, 0xFF}
}

// DetectOrder 根据流开头的两个 BOM 字节判定字节序；未识别时第二个返回值为 false。
func DetectOrder(a, b byte) (int, bool) {
	switch {
	case a == 0xFF && b == 0xFE:
		return LE, true
	case a == 0xFE && b == 0xFF:
		return BE, true
	default:
		return 0, false
	}
}
