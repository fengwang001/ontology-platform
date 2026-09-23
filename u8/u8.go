// Package u8 实现字节级 UTF-8 逐标量解码与编码，不使用 unicode/utf8 或任何隐式解码。
package u8

import "ontology/scalar"

// Event 是一次解码结果。OK 为真时 R 是合法标量；否则为一个非法单元。
// Start 是该单元在输入流中的起始偏移，Len 是其吞掉的字节数。
type Event struct {
	R     rune
	OK    bool
	Start int
	Len   int
}

// Decoder 是单遍 UTF-8 解码器。零值即可用。非并发安全。
type Decoder struct {
	pend   []byte // 已接受、尚未成单元的前缀（含首字节）
	start  int    // 当前单元的起始偏移
	need   int    // 还需续字节数
	refeed byte
	hasRF  bool
	checks int64
}

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Pending 返回缓存中尚未成单元的字节（副本）。
func (d *Decoder) Pending() []byte { return append([]byte(nil), d.pend...) }

func (d *Decoder) bad(len, start int) Event {
	d.pend, d.need = nil, 0
	return Event{Start: start, Len: len}
}

// Push 喂入一个字节（偏移 off）。e.OK=false 表示产生一个非法单元。
func (d *Decoder) Push(b byte, off int) (e Event, refeed bool) {
	d.checks++
	if d.need == 0 {
		n := scalar.LeadLen(b)
		if n == 0 {
			return Event{Start: off, Len: 1}, false
		}
		d.pend, d.start, d.need = []byte{b}, off, n-1
		return Event{}, false
	}
	pos := len(d.pend) - 1 // 0=第二字节
	if pos == 0 && !scalar.SecondRangeOK(d.pend[0], b) {
		rf := b
		e = d.bad(1, d.start)
		d.refeed, d.hasRF = rf, true
		return e, true
	}
	if pos > 0 && !scalar.IsContByte(b) {
		e = d.bad(len(d.pend), d.start)
		d.refeed, d.hasRF = b, true
		return e, true
	}
	d.pend = append(d.pend, b)
	d.need--
	if d.need == 0 {
		e = Event{R: decode(d.pend), OK: true, Start: d.start, Len: len(d.pend)}
		d.pend = nil
		return e, false
	}
	return Event{}, false
}

// Refeed 取出需要作为新首字节重新处理的字节；第二返回值为相对当前字节偏移
// （重处理的正是触发字节本身，相对为 0）。每次非法至多 1 个。
func (d *Decoder) Refeed() (byte, int, bool) {
	b, ok := d.refeed, d.hasRF
	d.hasRF = false
	return b, 0, ok
}

// Flush 在流结束时调用。返回的事件 OK 恒为 false（残留即一个非法单元）；
// truncated=true 表示残留是合法未完成前缀（截断），否则空残留。
func (d *Decoder) Flush() (e Event, truncated bool) {
	if len(d.pend) == 0 {
		return Event{}, false
	}
	return d.bad(len(d.pend), d.start), true
}

// ValidPrefix 报告 p 是否为“可继续等待”的合法 UTF-8 前缀（用于 par 对齐）。
func ValidPrefix(p []byte) bool {
	var d Decoder
	for i, b := range p {
		if _, rf := d.Push(b, i); rf {
			return false
		}
	}
	return len(d.pend) > 0
}

func decode(p []byte) rune {
	switch len(p) {
	case 2:
		return rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		return rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	default:
		return rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 | rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
}

// Encode 把合法标量 r 编码为 UTF-8 字节。
func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F, 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
}

// BOM 是 UTF-8 编码的 U+FEFF。
var BOM = []byte{0xEF, 0xBB, 0xBF}
