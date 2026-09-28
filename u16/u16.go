// Package u16 手写 UTF-16LE/BE 字节级解码/编码（不使用 unicode/utf16）。
package u16

import "ontology/scalar"

type Kind uint8

const (
	KRune Kind = iota // 合法标量（BMP 或代理对）
	KBad              // 一个非法单元：孤立/错配代理，长度 2
)

type Event struct {
	Kind Kind
	R    rune
	Off  int // 单元起始的字节偏移
	Len  int // 2（孤立代理）或 4（代理对）
}

// Decoder 跨 Write 维护半个码元与未配平高代理；每字节检查一次。
type Decoder struct {
	bo       byte // 0=LE,1=BE
	half     int  // -1 无；否则缓存的首字节
	hi       uint16
	hiOff    int
	hasHi    bool
	Examined int
}

// NewDecoder 返回初始状态的 UTF-16 解码器（默认小端）。
func NewDecoder() *Decoder { return &Decoder{half: -1} }

func (d *Decoder) Pending() int {
	n := 0
	if d.half >= 0 {
		n++
	}
	if d.hasHi {
		n += 2
	}
	return n
}

// SetBO 设置字节序（0=LE,1=BE）。
func (d *Decoder) SetBO(bo byte) { d.bo = bo }

// Feed 追加字节并对每个完整单元调用 cb。
func (d *Decoder) Feed(p []byte, offBase int, cb func(Event)) {
	for _, b := range p {
		d.Examined++
		off := offBase
		offBase++
		if d.half < 0 {
			d.half = int(b)
			continue
		}
		var u uint16
		if d.bo == 0 {
			u = uint16(d.half) | uint16(b)<<8
		} else {
			u = uint16(d.half)<<8 | uint16(b)
		}
		d.half = -1
		d.unit(u, off-1, cb)
	}
}

func (d *Decoder) unit(u uint16, off int, cb func(Event)) {
	switch {
	case d.hasHi && scalar.IsLow(u):
		cb(Event{KRune, scalar.JoinPair(d.hi, u), d.hiOff, 4})
		d.hasHi = false
	case d.hasHi: // 高代理后跟非低代理：高代理成非法单元，u 重新作为新字符
		cb(Event{KBad, scalar.Replacement, d.hiOff, 2})
		d.hasHi = false
		d.unit(u, off, cb)
	case scalar.IsHigh(u):
		d.hi, d.hiOff, d.hasHi = u, off, true
	case scalar.IsLow(u):
		cb(Event{KBad, scalar.Replacement, off, 2}) // 孤立低代理
	default:
		cb(Event{KRune, rune(u), off, 2})
	}
}

// Flush 报告残留：半个码元（Len1）或未配平高代理（Len2）。
func (d *Decoder) Flush(offBase int, cb func(Event)) {
	if d.hasHi {
		cb(Event{KBad, scalar.Replacement, d.hiOff, 2})
		d.hasHi = false
	}
	if d.half >= 0 {
		cb(Event{KBad, scalar.Replacement, offBase, 1})
		d.half = -1
	}
}

// EncodeRune 按给定字节序编码标量（>U+FFFF 编成代理对）。
func EncodeRune(r rune, bo byte) []byte {
	if !scalar.IsScalar(r) {
		return nil
	}
	put := func(u uint16) []byte {
		if bo == 0 {
			return []byte{byte(u), byte(u >> 8)}
		}
		return []byte{byte(u >> 8), byte(u)}
	}
	if r < 0x10000 {
		return put(uint16(r))
	}
	v := uint32(r) - 0x10000
	hi := uint16(0xD800 + v>>10)
	lo := uint16(0xDC00 + v&0x3FF)
	return append(put(hi), put(lo)...)
}
