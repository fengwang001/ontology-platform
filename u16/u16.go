// Package u16 提供手写的 UTF-16LE/BE 流式解码与编码。
package u16

import "ontology/scalar"

// Order 表示 UTF-16 字节序。
type Order uint8

const (
	UnknownEndian Order = iota
	LittleEndian
	BigEndian
)

// MaxPending 是解码器任意时刻缓存字节数的硬上限。
const MaxPending = 3

// BOM16 是字节序标记的代码单元值。
const BOM16 uint16 = 0xFEFF

// Unit 是一次解码结果。Size 为吞掉的字节数；CU 为代码单元数。
type Unit struct {
	R     rune
	Valid bool
	Size  int
	CU    int
	Start int64
	Trunc bool
	IsBOM bool
}

// Decoder 跨切分点解码 UTF-16 字节流。单实例非并发安全。
type Decoder struct {
	order    Order
	haveByte bool
	lo       byte
	high     bool
	highVal  rune
	highOff  int64
	off      int64
	started  bool
	Checked  int64
}

// NewDecoder 以给定字节序构造；UnknownEndian 时用流开头 BOM 嗅探。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o} }

// Order 返回最终确定的字节序。
func (d *Decoder) Order() Order { return d.order }

// PendingLen 返回缓存字节数：半字节(1)、高代理(2) 或二者(3)，≤3。
func (d *Decoder) PendingLen() int {
	n := 0
	if d.haveByte {
		n++
	}
	if d.high {
		n += 2
	}
	return n
}

// Feed 喂入新字节，base 为首字节全局偏移。
func (d *Decoder) Feed(in []byte, base int64) []Unit {
	var us []Unit
	for i, b := range in {
		at := base + int64(i)
		d.Checked++
		if !d.haveByte {
			d.lo, d.haveByte, d.off = b, true, at+1
			continue
		}
		d.haveByte = false
		cu := d.combine(d.lo, b)
		start := at - 1
		if d.high {
			us = d.resolveSurrogate(us, cu, d.highOff, start)
			continue
		}
		us = d.freshUnit(us, cu, start)
	}
	return us
}

// Flush 处理流结束残留：奇数字节或孤立高代理均为截断。
func (d *Decoder) Flush() (Unit, bool) {
	if d.haveByte {
		u := Unit{R: scalar.Replacement, Size: 1, Start: d.off - 1, Trunc: true}
		d.haveByte = false
		return u, true
	}
	if d.high {
		u := Unit{R: scalar.Replacement, Size: 2, Start: d.highOff, Trunc: true}
		d.high = false
		return u, true
	}
	return Unit{}, false
}

func (d *Decoder) combine(lo, hi byte) uint16 {
	if d.order == BigEndian {
		return uint16(lo)<<8 | uint16(hi)
	}
	return uint16(hi)<<8 | uint16(lo)
}

func (d *Decoder) freshUnit(us []Unit, cu uint16, start int64) []Unit {
	r := rune(cu)
	switch {
	case cu == BOM16 && !d.started:
		if d.order == UnknownEndian {
			if d.lo == 0xFE { // 输入顺序 FE,FF → 大端；FF,FE → 小端
				d.order = BigEndian
			} else {
				d.order = LittleEndian
			}
		}
		d.started = true
		return append(us, Unit{R: r, Valid: true, Size: 2, CU: 1, Start: start, IsBOM: true})
	case scalar.HighSurrogate(r):
		d.high, d.highVal, d.highOff = true, r, start
		return us
	case scalar.LowSurrogate(r):
		d.started = true
		return append(us, Unit{R: scalar.Replacement, Size: 2, CU: 1, Start: start})
	default:
		d.started = true
		return append(us, Unit{R: r, Valid: true, Size: 2, CU: 1, Start: start})
	}
}

func (d *Decoder) resolveSurrogate(us []Unit, cu uint16, highStart, start int64) []Unit {
	d.high = false
	r := rune(cu)
	if scalar.LowSurrogate(r) {
		dec := 0x10000 + (d.highVal-0xD800)<<10 + (r - 0xDC00)
		d.started = true
		return append(us, Unit{R: dec, Valid: true, Size: 4, CU: 2, Start: highStart})
	}
	us = append(us, Unit{R: scalar.Replacement, Size: 2, CU: 1, Start: highStart})
	return d.freshUnit(us, cu, start) // 非低代理：该单元重新当作新字符
}

// EncLen 返回标量编码为 UTF-16 占用的字节数（2 或 4）。
func EncLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// Encode 按指定字节序把合法标量追加到 dst（U+10000 以上编成代理对）。
func Encode(dst []byte, r rune, o Order) []byte {
	put := func(d []byte, cu uint16) []byte {
		hi, lo := byte(cu>>8), byte(cu)
		if o == BigEndian {
			return append(d, hi, lo)
		} else {
			return append(d, lo, hi)
		}
	}
	if r >= 0x10000 {
		v := uint32(r) - 0x10000
		dst = put(dst, uint16(0xD800+v>>10))
		dst = put(dst, uint16(0xDC00+v&0x3FF))
	} else {
		dst = put(dst, uint16(r))
	}
	return dst
}
