// Package u16 手写 UTF-16LE/BE 增量解码与编码（不使用 unicode/utf16）。
package u16

import "ontology/scalar"

// Order 标识字节序。
type Order int

const (
	LE Order = iota
	BE
	Auto // 仅流首 BOM 决定；无 BOM 默认 LE
)

// Kind 区分事件类型。
type Kind int

const (
	KindScalar Kind = iota
	KindInvalid
	KindTrunc
)

// Event 是一次解码结果，N 为吞掉的字节数，Off 为全局起始偏移。
type Event struct {
	Scalar rune
	N      int
	Kind   Kind
	Off    int64
}

// Decoder 是字节级增量解码器。
type Decoder struct {
	order    Order
	resolved bool
	hi       rune
	hasHi    bool
	hiStart  int64
	odd      byte
	hasOdd   bool
	oddStart int64
	first    bool
	// Checks 记录字节被检查的总次数。
	Checks int64
	fed    int64
}

// NewDecoder 创建解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o, hi: -1, first: true} }

func (d *Decoder) unit(a, b byte) rune {
	if d.order == BE {
		return rune(a)<<8 | rune(b)
	}
	return rune(b)<<8 | rune(a)
}

func (d *Decoder) feedUnit(u rune, off int64, cb func(Event) bool) bool {
	if d.first {
		d.first = false
		if u == scalar.BOM {
			return cb(Event{Scalar: scalar.BOM, N: 2, Kind: KindScalar, Off: off})
		}
	}
	switch {
	case scalar.IsHighSurrogate(u):
		if d.hasHi {
			if !cb(Event{N: 2, Kind: KindInvalid, Off: d.hiStart}) {
				return false
			}
		}
		d.hi, d.hasHi, d.hiStart = u, true, off
	case scalar.IsLowSurrogate(u):
		if d.hasHi {
			r, _ := scalar.SurrogatePair(d.hi, u)
			so := d.hiStart
			d.hi, d.hasHi = -1, false
			return cb(Event{Scalar: r, N: 4, Kind: KindScalar, Off: so})
		}
		if !cb(Event{N: 2, Kind: KindInvalid, Off: off}) {
			return false
		}
	default:
		if d.hasHi {
			if !cb(Event{N: 2, Kind: KindInvalid, Off: d.hiStart}) {
				return false
			}
			d.hi, d.hasHi = -1, false
		}
		if !cb(Event{Scalar: u, N: 2, Kind: KindScalar, Off: off}) {
			return false
		}
	}
	return true
}

// Feed 喂入原始字节；返回推进到的字节下标。
func (d *Decoder) Feed(p []byte, cb func(Event) bool) int {
	i := 0
	if d.hasOdd && len(p) > 0 {
		a, b, off := d.odd, p[0], d.oddStart
		d.hasOdd = false
		i = 1
		d.detect(a, b)
		d.Checks += 2
		if !d.feedUnit(d.unit(a, b), off, cb) {
			return 0
		}
	}
	for ; i+1 < len(p); i += 2 {
		d.Checks += 2
		if d.detect(p[i], p[i+1]); !d.feedUnit(d.unit(p[i], p[i+1]),
			d.fed+int64(i), cb) {
			return i
		}
	}
	if i < len(p) {
		d.odd, d.hasOdd, d.oddStart = p[i], true, d.fed+int64(i)
		d.Checks++
		i++
	}
	return i
}

func (d *Decoder) detect(a, b byte) {
	if d.resolved || d.order != Auto || !d.first {
		d.resolved = true
		return
	}
	d.resolved = true
	if a == 0xFE && b == 0xFF {
		d.order = BE
	} else if a == 0xFF && b == 0xFE {
		d.order = LE
	}
}

// Pending 返回缓存字节数（奇数尾 1 或高代理 2，合计至多 3）。
func (d *Decoder) Pending() int {
	n := 0
	if d.hasOdd {
		n++
	}
	if d.hasHi {
		n += 2
	}
	return n
}

// PendingStart 返回最早缓存字节的全局偏移。
func (d *Decoder) PendingStart() int64 {
	if d.hasOdd {
		return d.oddStart
	}
	if d.hasHi {
		return d.hiStart
	}
	return 0
}

// PendingBytes 按字节序拷贝返回缓存字节（奇数尾按原字节）。
func (d *Decoder) PendingBytes() []byte {
	if d.hasOdd {
		return []byte{d.odd}
	}
	if d.hasHi {
		return u(d.hi, d.order)
	}
	return nil
}

func u(r rune, o Order) []byte {
	if o == BE {
		return []byte{byte(r >> 8), byte(r)}
	}
	return []byte{byte(r), byte(r >> 8)}
}

// MarkFed 告知 n 个字节已归属。
func (d *Decoder) MarkFed(n int) { d.fed += int64(n) }

// End 报告截断：残留奇数尾或高代理（先奇数尾后高代理）。
func (d *Decoder) End(cb func(Event) bool) {
	if d.hasOdd {
		d.hasOdd = false
		cb(Event{N: 1, Kind: KindTrunc, Off: d.oddStart})
	}
	if d.hasHi {
		d.hasHi = false
		cb(Event{N: 2, Kind: KindTrunc, Off: d.hiStart})
	}
}

// Encode 把标量编码为 UTF-16 代码单元，追加到 dst。
func Encode(dst []byte, r rune, o Order) []byte {
	if r < 0x10000 {
		return put2(dst, uint16(r), o)
	}
	r -= 0x10000
	dst = put2(dst, uint16(0xD800+r>>10), o)
	return put2(dst, uint16(0xDC00+(r&0x3FF)), o)
}

func put2(dst []byte, x uint16, o Order) []byte {
	if o == BE {
		return append(dst, byte(x>>8), byte(x))
	}
	return append(dst, byte(x), byte(x>>8))
}
