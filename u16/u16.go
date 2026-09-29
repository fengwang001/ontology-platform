package u16

import "ontology/scalar"

// Order 是字节序。
type Order int

const (
	// LE 是小端。
	LE Order = iota
	// BE 是大端。
	BE
)

// EventKind 复用与 u8 相同的语义。
type EventKind uint8

const (
	EvScalar EventKind = iota
	EvInvalid
	EvTruncated
)

// Event 中 Len 的单位是**字节**（1 个 code unit = 2 字节）。
type Event struct {
	Kind EventKind
	R    rune
	Len  int
}

// Decoder 逐 code unit 解码。孤立/失配代理的当前 unit 不被消费。
type Decoder struct {
	order  Order
	hi     rune  // 待配对的高代理；-1 表示无
	odd    *byte // 奇数残留字节
}

// NewDecoder 创建解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o, hi: -1} }

// PendingLen 返回缓存字节数：0、1（奇数字节）或 2（高代理）。
func (d *Decoder) PendingLen() int {
	n := 0
	if d.odd != nil {
		n++
	}
	if d.hi >= 0 {
		n += 2
}
	return n
}

// StepByte 喂入一个字节；凑齐 unit 后解码。
func (d *Decoder) StepByte(b byte) (Event, bool) {
	if d.odd == nil {
		d.odd = new(byte)
		*d.odd = b
		return Event{}, false
	}
	lo := *d.odd
	d.odd = nil
	var u rune
	if d.order == LE {
		u = rune(lo) | rune(b)<<8
	} else {
		u = rune(b) | rune(lo)<<8
	}
	return d.StepUnit(u)
}

// StepUnit 解码一个完整 code unit。
func (d *Decoder) StepUnit(u rune) (Event, bool) {
	if d.hi >= 0 {
		if scalar.IsLowSurrogate(u) {
			r, _ := scalar.SurrogatePair(d.hi, u)
			d.hi = -1
			return Event{Kind: EvScalar, R: r, Len: 4}, true
		}
		d.hi = -1
		return Event{Kind: EvInvalid, Len: 2}, true // 当前 unit 未消费，回退
	}
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi = u
		return Event{}, false
	case scalar.IsLowSurrogate(u):
		return Event{Kind: EvInvalid, Len: 2}, true
	default:
		return Event{Kind: EvScalar, R: u, Len: 2}, true
	}
}

// Flush 处理流末残留。
func (d *Decoder) Flush() (Event, bool) {
	switch {
	case d.hi >= 0:
		d.hi = -1
		return Event{Kind: EvTruncated, Len: 2}, true
	case d.odd != nil:
		d.odd = nil
		return Event{Kind: EvTruncated, Len: 1}, true
	}
	return Event{}, false
}

// Encode 把标量按字节序追加为 UTF-16 字节。
func Encode(dst []byte, r rune, o Order) []byte {
	put := func(u uint16) {
		if o == LE {
			dst = append(dst, byte(u), byte(u>>8))
		} else {
			dst = append(dst, byte(u>>8), byte(u))
		}
	}
	if !scalar.IsScalar(r) {
		r = scalar.ReplacementChar
	}
	if r >= 0x10000 {
		hi, lo := scalar.SplitSurrogate(r)
		put(uint16(hi))
		put(uint16(lo))
	} else {
		put(uint16(r))
	}
	return dst
}

// EncodeLen 返回编码字节数。
func EncodeLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}
