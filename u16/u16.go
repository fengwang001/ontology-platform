// Package u16 做 UTF-16LE/BE 的增量解码与编码（含代理对与孤立代理）。
package u16

import "ontology/scalar"

// Event 是一次 UTF-16 单元结算。
type Event struct {
	R       rune
	Illegal bool
	N       int // 该单元占用的源字节数（代理对为 4，其余为 2）
}

// Decoder 逐字节增量解码；内部至多缓存 1 个待配对字节。
type Decoder struct {
	big    bool
	first  byte // 待配对的首字节
	have   bool
	hi     uint16 // 已见高代理，等待低代理
	hiSeen bool
	// Checks 为字节被检查次数（每物理字节 1 次）。
	Checks int64
}

// NewDecoder 构造解码器；big 选择端序（BOM 自动识别由上层完成）。
func NewDecoder(big bool) *Decoder { return &Decoder{big: big} }

func (d *Decoder) unit(lo, hi byte) uint16 {
	if d.big {
		return uint16(lo)<<8 | uint16(hi)
	}
	return uint16(hi)<<8 | uint16(lo)
}

// Feed 喂入一个字节，返回 0..2 个事件。
func (d *Decoder) Feed(b byte) []Event {
	d.Checks++
	if !d.have {
		d.first, d.have = b, true
		return nil
	}
	d.have = false
	return d.codeUnit(d.unit(d.first, b))
}

func (d *Decoder) codeUnit(c uint16) []Event {
	switch {
	case d.hiSeen && scalar.IsLowSurrogate(c):
		r, _ := scalar.DecodeSurrogatePair(d.hi, c)
		d.hiSeen = false
		return []Event{{R: r, N: 4}}
	case d.hiSeen:
		d.hiSeen = false
		evs := []Event{{R: scalar.ReplacementRune, Illegal: true, N: 2}} // 孤立高代理
		return append(evs, d.codeUnit(c)...)                             // 非低代理单元必须重新当字符处理
	case scalar.IsHighSurrogate(c):
		d.hi, d.hiSeen = c, true
		return nil
	case scalar.IsLowSurrogate(c):
		return []Event{{R: scalar.ReplacementRune, Illegal: true, N: 2}} // 孤立低代理
	default:
		return []Event{{R: rune(c), N: 2}}
	}
}

// Flush 在流结束时结算残尾。odd 为奇数字节截断；否则为孤立高代理截断。
func (d *Decoder) Flush() (ev Event, odd, orphan bool) {
	switch {
	case d.have:
		d.have = false
		return Event{R: scalar.ReplacementRune, Illegal: true}, true, false
	case d.hiSeen:
		d.hiSeen = false
		return Event{R: scalar.ReplacementRune, Illegal: true}, false, true
	default:
		return Event{}, false, false
	}
}

// Pending 返回尚未结算的残尾（0..1 字节）。
func (d *Decoder) Pending() []byte {
	if d.have {
		return []byte{d.first}
	}
	return nil
}

// Encode 把标量编码为指定端序的字节；非标量返回 nil。
func Encode(r rune, big bool) []byte {
	if !scalar.IsScalar(r) {
		return nil
	}
	put := func(c uint16) []byte {
		if big {
			return []byte{byte(c >> 8), byte(c)}
		}
		return []byte{byte(c), byte(c >> 8)}
	}
	if r < 0x10000 {
		return put(uint16(r))
	}
	hi, lo := scalar.EncodeSurrogatePair(r)
	return append(put(hi), put(lo)...)
}

// BOMLE / BOMBE 是两种端序的 BOM 字节。
var (
	BOMLE = []byte{0xFF, 0xFE}
	BOMBE = []byte{0xFE, 0xFF}
)
