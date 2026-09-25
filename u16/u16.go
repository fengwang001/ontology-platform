// Package u16 手工实现 UTF-16LE/BE 解码/编码，不使用 unicode/utf16。
package u16

import "ontology/scalar"

// Order 指定字节序；Auto 仅靠流首 BOM 判定，无 BOM 时按 LE。
type Order int

const (
	Auto Order = iota
	LE
	BE
)

// Unit 是一次解码结果。
type Unit struct {
	R       rune
	Start   int
	Size    int // 2（含孤立代理）或 4（代理对）
	Illegal bool
	Trunc   bool
	BOM     bool // 流首 BOM
}

// Decoder 是有状态 UTF-16 解码器；单实例非并发安全。
type Decoder struct {
	order     Order
	detected  bool
	hasByte   bool
	b0        byte
	b0pos     int
	hasHigh   bool
	high      rune
	highStart int
	replay    [2]byte
	replayPos [2]int
	nReplay   int
}

// NewDecoder 以给定字节序构造解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o} }

// Pending 返回尚未成单元的字节数（0、1 或 2：奇字节/高代理）。
func (d *Decoder) Pending() int {
	n := 0
	if d.hasByte {
		n++
	}
	if d.hasHigh {
		n += 2
	}
	return n
}

// PopReplay 返回需要重新解析的下一个字节（及其绝对偏移）。
func (d *Decoder) PopReplay() (byte, int, bool) {
	if d.nReplay == 0 {
		return 0, 0, false
	}
	b, pos := d.replay[0], d.replayPos[0]
	d.replay[0], d.replay[1] = d.replay[1], 0
	d.replayPos[0], d.replayPos[1] = d.replayPos[1], 0
	d.nReplay--
	return b, pos, true
}

func (d *Decoder) classify(cu rune, start int) (u Unit, holdHigh bool) {
	switch {
	case cu >= 0xD800 && cu <= 0xDBFF:
		return Unit{}, true
	case cu >= 0xDC00 && cu <= 0xDFFF:
		return Unit{R: scalar.RuneError, Start: start, Size: 2, Illegal: true}, false
	default:
		if scalar.Valid(cu) {
			return Unit{R: cu, Start: start, Size: 2}, false
		}
		return Unit{R: scalar.RuneError, Start: start, Size: 2, Illegal: true}, false
	}
}

// Push 喂入绝对偏移为 pos 的一个字节；完成时返回 ok=true。
func (d *Decoder) Push(b byte, pos int) (Unit, bool) {
	if !d.hasByte {
		d.hasByte, d.b0, d.b0pos = true, b, pos
		return Unit{}, false
	}
	d.hasByte = false
	cu := rune(d.b0)<<8 | rune(b)
	if d.order == BE {
		cu = rune(b)<<8 | rune(d.b0)
	}
	if d.order == Auto && !d.detected {
		d.detected = true
		if cu == 0xFEFF {
			d.order = LE
			return Unit{R: 0xFEFF, Start: d.b0pos, Size: 2, BOM: true}, true
		}
		if cu == 0xFFFE {
			d.order = BE
			return Unit{R: 0xFEFF, Start: d.b0pos, Size: 2, BOM: true}, true
		}
		d.order = LE
	}
	start := d.b0pos
	if d.hasHigh {
		d.hasHigh = false
		if cu >= 0xDC00 && cu <= 0xDFFF {
			r := 0x10000 + (d.high-0xD800)<<10 + (cu - 0xDC00)
			return Unit{R: r, Start: d.highStart, Size: 4}, true
		}
		d.replay, d.replayPos = [2]byte{d.b0, b}, [2]int{start, pos}
		d.nReplay = 2
		return Unit{R: scalar.RuneError, Start: d.highStart, Size: 2, Illegal: true}, true
	}
	u, hold := d.classify(cu, start)
	if hold {
		d.hasHigh, d.high, d.highStart = true, cu, start
		return Unit{}, false
	}
	return u, true
}

// Close 在流结束时调用。eof=false（par 内部段）时残留交给下一段。
func (d *Decoder) Close(eof bool) (Unit, bool) {
	if !eof {
		d.hasByte, d.hasHigh = false, false
		return Unit{}, false
	}
	switch {
	case d.hasByte:
		return Unit{R: scalar.RuneError, Start: d.b0pos, Size: 1, Illegal: true, Trunc: true}, true
	case d.hasHigh:
		return Unit{R: scalar.RuneError, Start: d.highStart, Size: 2, Illegal: true, Trunc: true}, true
	}
	return Unit{}, false
}

// Encode 把合法标量编码为 UTF-16（LE/BE）。
func Encode(r rune, o Order) []byte {
	put := func(cu rune) []byte {
		hi, lo := byte(cu>>8), byte(cu)
		if o == BE {
			return []byte{hi, lo}
		}
		return []byte{lo, hi}
	}
	if r >= 0x10000 {
		r -= 0x10000
		return append(put(0xD800+r>>10), put(0xDC00+r&0x3FF)...)
	}
	return put(r)
}
