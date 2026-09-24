// Package u8 在字节级实现 UTF-8 的逐标量解码与编码，
// 不使用 unicode/utf8 及任何隐式字符串解码。
package u8

import "ontology/scalar"

// MaxCache 是跨 Write 必须缓存的字节数硬上限（最长序列 4 字节，缓存其余 3 字节）。
const MaxCache = 3

// Unit 是一次解码结果：一个合法标量或一个非法单元。
type Unit struct {
	R       scalar.Rune
	Valid   bool
	Consume int // 该单元吞掉的字节数
}

// Decoder 是增量 UTF-8 解码器。单实例非并发安全。
type Decoder struct {
	buf    [4]byte
	nb     int
	need   int
	checks int64
}

// NewDecoder 创建解码器。
func NewDecoder() *Decoder { return &Decoder{} }

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// CacheLen 返回当前缓存字节数（恒 <= MaxCache）。
func (d *Decoder) CacheLen() int { return d.nb }

func cont(b byte) bool { return b&0xC0 == 0x80 }

// secondOK 报告首字节 lead 之后第二字节 b 是否在其合法区间（含非最短/越界判定）。
func secondOK(lead, b byte) bool {
	if !cont(b) {
		return false
	}
	switch {
	case lead == 0xE0:
		return b >= 0xA0
	case lead == 0xED:
		return b <= 0x9F
	case lead == 0xF0:
		return b >= 0x90
	case lead == 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

// Pump 喂入下一字节。返回的 Unit 数量为 0（该字节被缓存）或 1；
// 当 ok 为 false 时该字节未被消费，调用方必须用它再次调用 Pump。
func (d *Decoder) Pump(b byte) (Unit, bool) {
	d.checks++
	if d.nb == 0 {
		switch {
		case b < 0x80:
			return Unit{R: scalar.Rune(b), Valid: true, Consume: 1}, true
		case b >= 0xC2 && b <= 0xDF:
			d.buf[0], d.nb, d.need = b, 1, 2
		case b >= 0xE0 && b <= 0xEF:
			d.buf[0], d.nb, d.need = b, 1, 3
		case b >= 0xF0 && b <= 0xF4:
			d.buf[0], d.nb, d.need = b, 1, 4
		default: // 80..BF、C0、C1、F5..FF
			return Unit{R: scalar.Replacement, Valid: false, Consume: 1}, true
		}
		return Unit{}, true
	}
	lead := d.buf[0]
	if d.nb == 1 && !secondOK(lead, b) {
		d.nb, d.need = 0, 0
		return Unit{R: scalar.Replacement, Valid: false, Consume: 1}, false
	}
	if d.nb >= 2 && !cont(b) {
		n := d.nb
		d.nb, d.need = 0, 0
		return Unit{R: scalar.Replacement, Valid: false, Consume: n}, false
	}
	d.buf[d.nb] = b
	d.nb++
	if d.nb < d.need {
		return Unit{}, true
	}
	r := decodeFull(d.buf[:d.need])
	n := d.need
	d.nb, d.need = 0, 0
	return Unit{R: r, Valid: scalar.Valid(r), Consume: n}, true
}

func decodeFull(p []byte) scalar.Rune {
	switch len(p) {
	case 2:
		return scalar.Rune(p[0]&0x1F)<<6 | scalar.Rune(p[1]&0x3F)
	case 3:
		return scalar.Rune(p[0]&0x0F)<<12 | scalar.Rune(p[1]&0x3F)<<6 | scalar.Rune(p[2]&0x3F)
	default:
		return scalar.Rune(p[0]&0x07)<<18 | scalar.Rune(p[1]&0x3F)<<12 |
			scalar.Rune(p[2]&0x3F)<<6 | scalar.Rune(p[3]&0x3F)
	}
}

// Flush 在流结束时调用：残留的未完成前缀作为一个非法单元返回（吞掉缓存字节）。
func (d *Decoder) Flush() (Unit, bool) {
	if d.nb == 0 {
		return Unit{}, false
	}
	n := d.nb
	d.nb, d.need = 0, 0
	return Unit{R: scalar.Replacement, Valid: false, Consume: n}, true
}

// EncodeLen 返回标量 r 的 UTF-8 编码长度。
func EncodeLen(r scalar.Rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// Encode 把合法标量 r 写入 b（调用方保证 len(b) >= EncodeLen(r)）。
func Encode(b []byte, r scalar.Rune) {
	switch {
	case r < 0x80:
		b[0] = byte(r)
	case r < 0x800:
		b[0] = 0xC0 | byte(r>>6)
		b[1] = 0x80 | byte(r)&0x3F
	case r < 0x10000:
		b[0] = 0xE0 | byte(r>>12)
		b[1] = 0x80 | byte(r>>6)&0x3F
		b[2] = 0x80 | byte(r)&0x3F
	default:
		b[0] = 0xF0 | byte(r>>18)
		b[1] = 0x80 | byte(r>>12)&0x3F
		b[2] = 0x80 | byte(r>>6)&0x3F
		b[3] = 0x80 | byte(r)&0x3F
	}
}
