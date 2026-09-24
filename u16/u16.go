// Package u16 提供 UTF-16LE/UTF-16BE 的逐单元解码与编码，不使用 unicode/utf16。
package u16

import "ontology/scalar"

// 字节序。
const (
	LE = iota
	BE
)

// 复用 u8 包风格的事件语义。
const (
	EvScalar = iota
	EvNeed
	EvBad
)

// Event 是一次解码事件。UnitLen 对代理对为 4，孤立/普通单元为 2。
type Event struct {
	Kind    int
	R       rune
	UnitLen int
}

// Decoder 跨 Write 保留奇数尾字节、未配对高代理、以及判错后退回的单元。
type Decoder struct {
	order  int
	odd    byte
	hasOdd bool
	hi     uint16
	hasHi  bool
	replay [2]byte // 高代理判错后，须退回重解析的一个 code unit
	nRe    int
	Checks int
}

// NewDecoder 以固定字节序构造解码器。
func NewDecoder(order int) *Decoder { return &Decoder{order: order} }

// PendingLen 返回缓存字节数（硬上限 3：1 奇字节，或 2 字节待解析/高代理）。
func (d *Decoder) PendingLen() int {
	n := d.nRe
	if d.hasOdd {
		n++
	}
	if d.hasHi {
		n += 2
	}
	return n
}

// Reset 清空状态。
func (d *Decoder) Reset() {
	d.hasOdd, d.hasHi, d.nRe, d.Checks = false, false, 0, 0
}

// Drain 取出当前缓存的待解析字节（高代理/奇字节/回放单元）并清空解析状态，
// 追加到 out。返回的字节按解码器输入时的原始顺序排列。
func (d *Decoder) Drain(out []byte) []byte {
	if d.nRe == 2 {
		out = append(out, d.replay[0], d.replay[1])
	}
	if d.hasHi {
		hi := d.hi
		if d.order == LE {
			out = append(out, byte(hi), byte(hi>>8))
		} else {
			out = append(out, byte(hi>>8), byte(hi))
		}
	}
	if d.hasOdd {
		out = append(out, d.odd)
	}
	d.hasOdd, d.hasHi, d.nRe = false, false, 0
	return out
}

func (d *Decoder) unit(a, b byte) uint16 {
	if d.order == LE {
		return uint16(a) | uint16(b)<<8
	}
	return uint16(a)<<8 | uint16(b)
}

// handle 处理一个 code unit。units 为被吞掉的 code unit 数；
// reject 非空表示该单元未被吞掉，须退回重解析；pending 表示高代理被缓存。
func (d *Decoder) handle(u uint16) (ev Event, units int, reject *[2]byte, pending bool) {
	d.Checks++
	if d.hasHi {
		if scalar.IsLowSurrogate(u) {
			d.hasHi = false
			return Event{Kind: EvScalar, R: scalar.FromSurrogatePair(d.hi, u), UnitLen: 4}, 1, nil, false
		}
		d.hasHi = false
		return Event{Kind: EvBad, UnitLen: 2}, 0, &d.replay, false
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi, d.hasHi = u, true
		return Event{}, 1, nil, true
	case scalar.IsLowSurrogate(u):
		return Event{Kind: EvBad, UnitLen: 2}, 1, nil, false
	default:
		return Event{Kind: EvScalar, R: rune(u), UnitLen: 2}, 1, nil, false
	}
}

// Step 从 p 解析一个事件，consumed 为取自 p 的字节数（不含内部重放）。
func (d *Decoder) Step(p []byte) (Event, int) {
	consumed := 0
	var a, b byte
	have := false
	if d.nRe == 2 {
		a, b, have, d.nRe = d.replay[0], d.replay[1], true, 0
	}
	for {
		if !have {
			if d.hasOdd {
				if len(p) == 0 {
					return Event{Kind: EvNeed}, consumed
				}
				a, b = d.odd, p[0]
				d.hasOdd = false
				consumed++
				p = p[1:]
			} else {
				if len(p) < 2 {
					if len(p) == 1 {
						d.odd, d.hasOdd = p[0], true
						consumed++
					}
					return Event{Kind: EvNeed}, consumed
				}
				a, b = p[0], p[1]
				consumed += 2
				p = p[2:]
			}
		}
		have = false
		ev, units, reject, pending := d.handle(d.unit(a, b))
		switch {
		case reject != nil:
			reject[0], reject[1] = a, b
			d.nRe = 2
			return ev, consumed
		case pending:
			continue
		default:
			_ = units
			return ev, consumed
		}
	}
}

// Flush 处理流结束残留：奇字节或未配对高代理均为截断式非法单元。
func (d *Decoder) Flush() (Event, bool) {
	if d.hasHi {
		d.hasHi = false
		return Event{Kind: EvBad, UnitLen: 2}, true
	}
	if d.hasOdd {
		d.hasOdd = false
		return Event{Kind: EvBad, UnitLen: 1}, true
	}
	return Event{}, false
}

// EncodeLen 返回标量占用的 UTF-16 字节数（代理对 4，其余 2）。
func EncodeLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// Encode 把标量写入 b（容量须 ≥ EncodeLen）。
func Encode(b []byte, r rune, order int) int {
	put := func(at int, u uint16) {
		if order == LE {
			b[at], b[at+1] = byte(u), byte(u>>8)
		} else {
			b[at], b[at+1] = byte(u>>8), byte(u)
		}
	}
	if r >= 0x10000 {
		hi, lo := scalar.ToSurrogatePair(r)
		put(0, hi)
		put(2, lo)
		return 4
	}
	put(0, uint16(r))
	return 2
}

// BOM 返回指定字节序下 BOM(U+FEFF) 的两个字节。
func BOM(order int) (byte, byte) {
	if order == BE {
		return 0xFE, 0xFF
	}
	return 0xFF, 0xFE
}
