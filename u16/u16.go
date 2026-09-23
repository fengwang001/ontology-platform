// Package u16 在字节级别实现增量 UTF-16LE/BE 解码与编码，不使用 unicode/utf16。
package u16

import "ontology/scalar"

const (
	KindOK    = 0
	KindBad   = 1
	KindTrunc = 2
)

// LE / BE 选择字节序。
const (
	LE = iota
	BE
)

// Event 是一个解码单元：合法标量、非法代理单元或截断。Len 为吞掉的字节数。
type Event struct {
	Kind int
	Rune scalar.Rune
	Len  int
}

// Decoder 是增量 UTF-16 状态机。order 固定后才产出事件。
type Decoder struct {
	order   int
	ordSet  bool
	low     byte // 奇数残留字节
	hasLow  bool
	hi      scalar.Rune
	hasHi   bool
	queued  Event
	hasQ    bool
	qUnit   scalar.Rune // 高代理失败后需重新解释的单元
	qReady  bool
	checks  int64
}

// NewDecoder 创建解码器；order 为假定字节序，可被开头 BOM 覆盖。
func NewDecoder(order int) *Decoder { return &Decoder{order: order} }

// Checks 返回字节被检查的次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Pending 返回未决缓冲字节数（奇数残留 1，加待配对高代理 2，上限 3）。
func (d *Decoder) Pending() int {
	n := 0
	if d.hasLow {
		n++
	}
	if d.hasHi || d.qReady {
		n += 2
	}
	return n
}

func (d *Decoder) unitFromBytes(a, b byte) scalar.Rune {
	if d.order == LE {
		return scalar.Rune(a) | scalar.Rune(b)<<8
	}
	return scalar.Rune(b) | scalar.Rune(a)<<8
}

// Feed 喂入一个字节，返回可能的事件；无事件时 Len==0。
func (d *Decoder) Feed(b byte) Event {
	d.checks++
	if d.qReady { // 重新解释此前被高代理占用的单元
		d.qReady = false
		return d.unitEvent(d.qUnit)
	}
	if !d.hasLow {
		d.low, d.hasLow = b, true
		return Event{}
	}
	d.hasLow = false
	u := d.unitFromBytes(d.low, b)
	if d.hasHi {
		d.hasHi = false
		if scalar.IsLowSurrogate(u) {
			return Event{Kind: KindOK, Rune: scalar.CombineSurrogates(d.hi, u), Len: 4}
		}
		// 孤立高代理；当前单元作为新字符重新解释，不被吞掉。
		d.qUnit, d.qReady = u, true
		return Event{Kind: KindBad, Len: 2}
	}
	return d.unitEvent(u)
}

func (d *Decoder) unitEvent(u scalar.Rune) Event {
	switch {
	case scalar.IsHighSurrogate(u):
		d.hi, d.hasHi = u, true
		return Event{}
	case scalar.IsLowSurrogate(u):
		return Event{Kind: KindBad, Len: 2}
	default:
		return Event{Kind: KindOK, Rune: u, Len: 2}
	}
}

// SetOrder 用于 BOM 确定字节序后切换。
func (d *Decoder) SetOrder(o int) { d.order, d.ordSet = o, true }

// Flush 处理流结束：奇数字节或残留高代理均为截断。
func (d *Decoder) Flush() Event {
	switch {
	case d.hasLow:
		d.hasLow = false
		return Event{Kind: KindTrunc, Len: 1}
	case d.hasHi:
		d.hasHi = false
		return Event{Kind: KindTrunc, Len: 2}
	default:
		return Event{}
	}
}

// Encode 把标量编码为 UTF-16 追加到 dst（BOM 由调用方以 U+FEFF 处理）。
func Encode(dst []byte, order int, r scalar.Rune) []byte {
	put := func(u uint16) {
		if order == LE {
			dst = append(dst, byte(u), byte(u>>8))
		} else {
			dst = append(dst, byte(u>>8), byte(u))
		}
	}
	if r >= 0x10000 {
		r -= 0x10000
		put(0xD800 + uint16(r>>10))
		put(0xDC00 + uint16(r&0x3FF))
		return dst
	}
	put(uint16(r))
	return dst
}
