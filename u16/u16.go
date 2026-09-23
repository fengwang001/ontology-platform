package u16

import (
	"encoding/binary"

	"ontology/scalar"
)

type Unit struct {
	R      rune
	Valid  bool
	Start  int64
	Length int // 吞掉的输入字节数
}

const (
	LE = iota
	BE
)

// Decoder 解码指定字节序的 UTF-16；BOM 由上层（stream）处理。
type Decoder struct {
	checks *int64
	order  binary.ByteOrder
	pos    int64
	odd    byte // 奇数残留字节
	hasOdd bool
	hi     uint16
	hiAt   int64
	hasHi  bool
}

func NewDecoder(order int, checks *int64) *Decoder {
	var bo binary.ByteOrder = binary.LittleEndian
	if order == BE {
		bo = binary.BigEndian
	}
	return &Decoder{checks: checks, order: bo}
}

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

func (d *Decoder) Feed(p []byte, cb func(Unit)) int {
	if d.hasOdd && len(p) > 0 {
		var b [2]byte
		b[0], b[1] = d.odd, p[0]
		d.hasOdd = false
		d.unit(d.order.Uint16(b[:]), d.pos-1, cb)
		p = p[1:]
		d.pos++
		if d.checks != nil {
			*d.checks++
		}
	}
	i := 0
	for ; i+1 < len(p); i += 2 {
		if d.checks != nil {
			*d.checks += 2
		}
		u := d.order.Uint16(p[i : i+2])
		d.pos += 2
		d.unit(u, d.pos-2, cb)
	}
	if i < len(p) {
		d.odd, d.hasOdd = p[i], true
		d.pos++
		i++
		if d.checks != nil {
			*d.checks++
		}
	}
	return i
}

func (d *Decoder) unit(u uint16, at int64, cb func(Unit)) {
	switch {
	case d.hasHi:
		if scalar.LowSurrogate(u) {
			cb(Unit{R: scalar.Pair(d.hi, u), Valid: true, Start: d.hiAt, Length: 4})
			d.hasHi = false
			return
		}
		cb(Unit{Start: d.hiAt, Length: 2}) // 孤立高代理；当前单元留给下一轮
		d.hasHi = false
		d.unit(u, at, cb)
	case scalar.HighSurrogate(u):
		d.hi, d.hiAt, d.hasHi = u, at, true
	case scalar.SurrogateCode(u): // 孤立低代理
		cb(Unit{Start: at, Length: 2})
	default:
		cb(Unit{R: rune(u), Valid: true, Start: at, Length: 2})
	}
}

// Finish：残留奇数字节或高代理均属截断，各自报一个非法单元。
func (d *Decoder) Finish(cb func(Unit)) {
	if d.hasHi {
		cb(Unit{Start: d.hiAt, Length: 2})
		d.hasHi = false
	}
	if d.hasOdd {
		cb(Unit{Start: d.pos - 1, Length: 1})
		d.hasOdd = false
	}
}

// Encode 写入 r 的 UTF-16 表示，返回字节数（2 或 4）。
func Encode(order int, r rune, out []byte) int {
	var bo binary.ByteOrder = binary.LittleEndian
	if order == BE {
		bo = binary.BigEndian
	}
	if r <= 0xFFFF {
		bo.PutUint16(out, uint16(r))
		return 2
	}
	hi, lo := scalar.SplitPair(r)
	bo.PutUint16(out, hi)
	bo.PutUint16(out[2:], lo)
	return 4

// BOM 标记（FEFF）的两种字节序字节。
var bomLE = [2]byte{0xFF, 0xFE}
var bomBE = [2]byte{0xFE, 0xFF}

// DetectOrder 仅当开头是完整 BOM 时返回字节序、true 与 BOM 长度。
func DetectOrder(p []byte) (order int, ok bool, n int) {
	if len(p) >= 2 {
		if p[0] == bomLE[0] && p[1] == bomLE[1] {
			return LE, true, 2
		}
		if p[0] == bomBE[0] && p[1] == bomBE[1] {
			return BE, true, 2
		}
	}
	return 0, false, 0
}
