// Package u16 在字节级实现 UTF-16LE/BE 的解码与编码，依赖 scalar。
package u16

import "ontology/scalar"

const (
	OrderAuto = 0 // 开头无 BOM 时按 LE
	OrderLE   = 1
	OrderBE   = 2
)

// Event 是一次解码产出：标量、非法单元、截断残留或开头 BOM。
type Event struct {
	Rune    rune
	Illegal bool
	EOF      bool
	BOM      bool
	Len      int // 该事件吞掉的输入字节数
}

// Decoder 是字节级 UTF-16 状态机。
type Decoder struct {
	order    byte
	pend     byte
	havePend bool
	hi       uint16
	haveHi   bool
	started  bool // 是否已处理过第一个完整码元
}

func NewDecoder(order byte) *Decoder { return &Decoder{order: order} }

// Order 返回确定后的字节序（喂入 BOM 后可能变化）。
func (d *Decoder) Order() byte {
	if d.order == OrderAuto { return OrderLE }
	return d.order
}

// PendingLen 返回未配对字节数（0 或 1）。
func (d *Decoder) PendingLen() int {
	n := 0
	if d.havePend { n++ }
	if d.haveHi { n += 2 }
	return n
}

// Prime 静默推进状态（并行段引导），不产出事件。
func (d *Decoder) Prime(b byte) {
	ev, _ := d.Feed(b)
	_ = ev
}

// Feed 喂入一个字节，返回最多一个事件；事件为 nil 表示尚无完整单元。
func (d *Decoder) Feed(b byte) (*Event, bool) {
	if !d.havePend {
		d.pend, d.havePend = b, true
		return nil, true
	}
	d.havePend = false
	var lo, hi16 byte = d.pend, b
	if d.Order() == OrderBE { lo, hi16 = b, d.pend }
	c := uint16(hi16)<<8 | uint16(lo)
	if !d.started {
		d.started = true
		if c == 0xFEFF {
			if d.order == OrderAuto { d.order = OrderLE }
			return &Event{Rune: scalar.BOM, BOM: true, Len: 2}, true
		}
		if c == 0xFFFE {
			if d.order == OrderAuto { d.order = OrderBE }
			return &Event{Rune: scalar.BOM, BOM: true, Len: 2}, true
		}
	}
	switch {
	case scalar.IsHighSurrogate(c):
		if d.haveHi {
			old := d.hi
			d.hi = c
			return &Event{Rune: scalar.Replacement, Illegal: true, Len: 2}, oldEventConsumed(old)
		}
		d.hi, d.haveHi = c, true
		return nil, true
	case scalar.IsLowSurrogate(c):
		if d.haveHi {
			r := scalar.SurrogatePair(d.hi, c)
			d.haveHi = false
			return &Event{Rune: r, Len: 4}, true
		}
		return &Event{Rune: scalar.Replacement, Illegal: true, Len: 2}, true
	default:
		if d.haveHi {
			d.haveHi = false
			return &Event{Rune: scalar.Replacement, Illegal: true, Len: 2}, false // 重放当前码元
		}
		return &Event{Rune: rune(c), Len: 2}, true
	}
}

func oldEventConsumed(uint16) bool { return true }

// Close 报告残留：奇数字节或未配对高代理均为截断。
func (d *Decoder) Close() *Event {
	if d.haveHi {
		d.haveHi = false
		e := &Event{Rune: scalar.Replacement, Illegal: true, EOF: true, Len: 2}
		if d.havePend { d.havePend = false }
		return e
	}
	if d.havePend {
		d.havePend = false
		return &Event{Rune: scalar.Replacement, Illegal: true, EOF: true, Len: 1}
	}
	return nil
}

// Encode 把标量编码成 UTF-16 字节；bo 为 OrderLE/OrderBE。
func Encode(r rune, bo byte) []byte {
	var units []uint16
	if r >= 0x10000 {
		hi, lo := scalar.SplitSurrogate(r)
		units = []uint16{hi, lo}
	} else {
		units = []uint16{uint16(r)}
	}
	out := make([]byte, 0, len(units)*2)
	for _, c := range units {
		if bo == OrderBE {
			out = append(out, byte(c>>8), byte(c))
		} else {
			out = append(out, byte(c), byte(c>>8))
		}
	}
	return out
}
