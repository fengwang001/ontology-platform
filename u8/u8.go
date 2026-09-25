// Package u8 提供字节级 UTF-8 逐标量解码与编码。
package u8

import "ontology/scalar"

// Decoder 是增量 UTF-8 状态机；每字节至多检查一次，不回扫。
type Decoder struct{}

// NewDecoder 创建解码器。
func NewDecoder() *Decoder { return &Decoder{} }

// Kind 是事件类别。
type Kind int

const (
	None Kind = iota
	Need
	Ready
	Bad
	Incomplete
)

// Event 是 Push 的结果。Rune 仅 Ready 时有效。
type Event struct {
	Kind   Kind
	Rune   scalar.Rune
	Length int // Ready/Bad 单元的输入字节数
}

// Decoder 是增量 UTF-8 状态机；每字节至多检查一次，不回扫。
type Decoder struct {
	need, got int
	min2, max2 byte
	cur        scalar.Rune
	held       byte
	haveHeld   bool
}

// MaxPending 是合法前缀缓存的硬上限字节数。
const MaxPending = 3

// Push 喂入一个字节，返回本次事件。Bad 后坏字节可能被挂起，见 Held。
func (d *Decoder) Push(b byte) Event {
	if d.need == 0 {
		d.haveHeld = false
		if b < 0x80 {
			return Event{Ready, scalar.Rune(b), 1}
		}
		switch {
		case b >= 0xC2 && b <= 0xDF:
			d.need, d.got, d.cur = 1, 1, scalar.Rune(b&0x1F)
		case b == 0xE0:
			d.need, d.got, d.cur, d.min2, d.max2 = 2, 1, scalar.Rune(b&0x0F), 0xA0, 0xBF
		case b >= 0xE1 && b <= 0xEC:
			d.need, d.got, d.cur, d.min2, d.max2 = 2, 1, scalar.Rune(b&0x0F), 0x80, 0xBF
		case b == 0xED:
			d.need, d.got, d.cur, d.min2, d.max2 = 2, 1, scalar.Rune(b&0x0F), 0x80, 0x9F
		case b >= 0xEE && b <= 0xEF:
			d.need, d.got, d.cur, d.min2, d.max2 = 2, 1, scalar.Rune(b&0x0F), 0x80, 0xBF
		case b == 0xF0:
			d.need, d.got, d.cur, d.min2, d.max2 = 3, 1, scalar.Rune(b&0x07), 0x90, 0xBF
		case b >= 0xF1 && b <= 0xF3:
			d.need, d.got, d.cur, d.min2, d.max2 = 3, 1, scalar.Rune(b&0x07), 0x80, 0xBF
		case b == 0xF4:
			d.need, d.got, d.cur, d.min2, d.max2 = 3, 1, scalar.Rune(b&0x07), 0x80, 0x8F
		default: // 80..BF 裸延续、C0 C1、F5..FF
			return Event{Bad, 0xFFFD, 1}
		}
		return Event{Kind: Need}
	}
	pos := d.got // 当前字节在序列中的位置（1 表示第二字节）
	if b < 0x80 || b > 0xBF || (pos == 1 && (b < d.min2 || b > d.max2)) {
		n := d.got // 首字节 + 已合法吞下的延续字节
		d.need = 0
		d.held, d.haveHeld = b, true
		return Event{Bad, 0xFFFD, n}
	}
	d.cur = d.cur<<6 | scalar.Rune(b&0x3F)
	d.need--
	d.got++
	if d.need == 0 {
		r := d.cur
		n := d.got
		d.need, d.got = 0, 0
		return Event{Ready, r, n}
	}
	return Event{Kind: Need}
}

// Held 报告是否有一个坏字节被挂起等待重新解析。
func (d *Decoder) Held() (byte, bool) { return d.held, d.haveHeld }

// DropHeld 取走挂起的坏字节并以新字符重新解析，返回新事件，不增加检查计数。
func (d *Decoder) DropHeld() Event {
	b := d.held
	d.haveHeld = false
	return d.Push(b)
}

// Flush 在流结束时调用，报告未完成前缀。
func (d *Decoder) Flush() Event {
	if d.need > 0 {
		n := d.got
		d.need, d.got = 0, 0
		return Event{Incomplete, 0xFFFD, n}
	}
	return Event{Kind: None}
}

// Reset 回到初始状态。
func (d *Decoder) Reset() {}

// Encode 把一个合法标量编码为 UTF-8 字节。
func Encode(r scalar.Rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F,
			0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
}

// RuneLen 返回标量的 UTF-8 编码字节数。
func RuneLen(r scalar.Rune) int {
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
