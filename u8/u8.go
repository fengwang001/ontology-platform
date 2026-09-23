package u8

import (
	"ontology/scalar"
)

// Unit 是一次解码结果：合法标量或一个非法单元。
type Unit struct {
	R      rune
	Valid  bool
	Start  int64 // 在整个输入流中的起始偏移
	Length int   // 该单元吞掉的字节数
}

// Decoder 是有状态的逐字节 UTF-8 解码器；单实例非并发安全。
// 已缓存的前缀字节不会被重复检查，因此总检查次数 ≤ 2×输入字节数。
type Decoder struct {
	checks *int64
	pos    int64
	st     int   // 0=空闲 1=已见首字节 2=已见合法第二字节
	lead   byte  // 首字节
	b2     byte  // 第二字节
	need   int   // 还需续字节数
	got    int   // 已收续字节数
	start  int64 // 当前未完成单元起点
}

func NewDecoder(checks *int64) *Decoder { return &Decoder{checks: checks} }

// Pending 返回缓存中的半成品字节数（上限 3）。
func (d *Decoder) Pending() int {
	if d.st == 0 {
		return 0
	}
	return 2 + d.got
}

func (d *Decoder) check(b byte) bool {
	if d.checks != nil {
		*d.checks++
	}
	return b&0xC0 == 0x80
}

// Feed 消费 p，对每个完整单元回调 cb；返回从 p 读走的字节数。
func (d *Decoder) Feed(p []byte, cb func(Unit)) int {
	i := 0
	fail := func(n int) {
		cb(Unit{Start: d.start, Length: n})
		d.st = 0
	}
	for i < len(p) {
		b := p[i]
		if d.checks != nil {
			*d.checks++
		}
		i++
		d.pos++
		switch d.st {
		case 0:
			d.start = d.pos
			ln := scalar.LeadLen(b)
			if ln == 0 {
				cb(Unit{Start: d.start, Length: 1})
				continue
			}
			if ln == 1 {
				cb(Unit{R: rune(b), Valid: true, Start: d.start, Length: 1})
				continue
			}
			d.lead, d.need, d.got, d.st, d.b2 = b, ln-2, 0, 1, 0
		case 1:
			if !scalar.SecondOK(d.lead, b) {
				fail(1)
				i-- // 该字节作为新首字节重新解析
				d.pos--
				continue
			}
			d.b2, d.st = b, 2
		default:
			if !d.check(b) {
				fail(2 + d.got)
				i--
				d.pos--
				continue
			}
			d.got++
			if d.got == d.need {
				cb(Unit{R: decode(d.lead, d.b2, p[i-d.got:i]), Valid: true, Start: d.start, Length: 2 + d.got})
				d.st = 0
			}
		}
	}
	return i
}

func decode(lead, b2 byte, tail []byte) rune {
	r := rune(lead&0x0F) << 6
	r |= rune(b2 & 0x3F)
	for _, b := range tail {
		r = r<<6 | rune(b&0x3F)
	}
	return r
}

// Finish 处理流结束：残留半成品作为一个非法单元；无残留则不回调。
func (d *Decoder) Finish(cb func(Unit)) {
	if d.st != 0 {
		n := 1
		if d.st == 2 {
			n = 2 + d.got
		}
		cb(Unit{Start: d.start, Length: n})
		d.st = 0
	}
}

// Encode 手写 UTF-8 编码，返回字节长度。
func Encode(r rune, out []byte) int {
	switch {
	case r < 0x80:
		out[0] = byte(r)
		return 1
	case r < 0x800:
		out[0] = 0xC0 | byte(r>>6)
		out[1] = 0x80 | byte(r)&0x3F
		return 2
	case r < 0x10000:
		out[0] = 0xE0 | byte(r>>12)
		out[1] = 0x80 | byte(r>>6)&0x3F
		out[2] = 0x80 | byte(r)&0x3F
		return 3
	default:
		out[0] = 0xF0 | byte(r>>18)
		out[1] = 0x80 | byte(r>>12)&0x3F
		out[2] = 0x80 | byte(r>>6)&0x3F
		out[3] = 0x80 | byte(r)&0x3F
		return 4
	}
}

var BOM = []byte{0xEF, 0xBB, 0xBF}
