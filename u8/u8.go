// Package u8 是手写的 UTF-8 字节级解码器与编码器。
// 不使用 unicode/utf8，也不使用任何隐式 rune 解码。
package u8

import "ontology/scalar"

// 事件种类。
const (
	OK  = iota // 一个合法标量
	Bad        // 一个非法单元（已吞掉 Len 字节）
)

// Event 是一次字节输入产生的结果。Kind 为 OK/Bad；R 为标量或
// scalar.RuneError；Len 为该单元吞掉的字节数；Start 为该单元相对
// 解码器已见输入流的起始字节偏移。
type Event struct {
	Kind  int
	R     rune
	Len   int
	Start int64
	// Repush 为 true 时，喂入的字节不属于本非法单元，必须重新喂入。
	Repush bool
}

// Decoder 是单字节推进的 UTF-8 状态机。挂起字节不超过 3 个。
type Decoder struct {
	buf   [3]byte // 已缓存的续字节（不含首字节）
	first byte    // 首字节
	need  int     // 还需续字节数；0 表示 accept
	total int     // 序列总长度
	cp    rune    // 已累计码点
	seen  int64   // 已见输入字节总数
	start int64   // 当前序列起始偏移
}

// Reset 清空状态机。
func (d *Decoder) Reset() { *d = Decoder{} }

// Pending 返回挂起缓存中的字节数（0..3）。
func (d *Decoder) Pending() int {
	if d.need == 0 {
		return 0
	}
	return d.total - d.need
}

// PendingBytes 按接收顺序返回挂起字节（含首字节），副本。
func (d *Decoder) PendingBytes() []byte {
	n := d.Pending()
	if n == 0 {
		return nil
	}
	out := make([]byte, n)
	out[0] = d.first
	copy(out[1:], d.buf[:n-1])
	return out
}

func (d *Decoder) bad(n int) Event {
	e := Event{Kind: Bad, R: scalar.RuneError, Len: n, Start: d.start}
	d.need = 0
	return e
}

// Feed 喂入一个字节并返回事件（无事件时 ok 为 false，字节被挂起）。
func (d *Decoder) Feed(b byte) (e Event, ok bool) {
	if d.need > 0 {
		d.seen++
		pos := d.total - d.need // 当前是序列的第几字节（1 起）
		if !scalar.Continuation(b) {
			// 等待序列在该字节处死亡：只吞已收前缀，b 留给上层重喂。
			d.seen--
			e := d.bad(pos)
			e.Repush = true
			return e, true
		}
		d.cp = d.cp<<6 | rune(b&0x3F)
		// 位置相关的越界检查（非最短形式 / 代理 / 越界）。
		bad := false
		switch d.first {
		case 0xE0:
			bad = pos == 2 && b < 0xA0
		case 0xED:
			bad = pos == 2 && b > 0x9F
		case 0xF0:
			bad = pos == 2 && b < 0x90
		case 0xF4:
			bad = pos == 2 && b > 0x8F
		}
		if bad {
			return d.bad(pos + 1), true // 含当前字节
		}
		d.buf[pos-1] = b
		d.need--
		if d.need == 0 {
			e = Event{Kind: OK, R: d.cp, Len: d.total, Start: d.start}
			d.need = 0
			return e, true
		}
		return Event{}, false
	}
	d.seen++
	d.start = d.seen - 1
	d.cp = 0
	switch {
	case b < 0x80:
		return Event{Kind: OK, R: rune(b), Len: 1, Start: d.start}, true
	case b >= 0xC2 && b <= 0xDF:
		d.first, d.total, d.need, d.cp = b, 2, 1, rune(b&0x1F)
	case b == 0xE0 || b >= 0xE1 && b <= 0xEF:
		d.first, d.total, d.need, d.cp = b, 3, 2, rune(b&0x0F)
	case b == 0xF0 || b >= 0xF1 && b <= 0xF4:
		d.first, d.total, d.need, d.cp = b, 4, 3, rune(b&0x07)
	default: // 80..BF、C0 C1、F5..FF
		return d.bad(1), true
	}
	return Event{}, false
}

// Finish 在流结束时调用：有挂起前缀则它整体是一个截断/非法单元。
func (d *Decoder) Finish() (Event, bool) {
	if d.need == 0 {
		return Event{}, false
	}
	return d.bad(d.Pending()), true
}

// Encode 把一个标量编码成 UTF-8 字节追加到 dst。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}

// Len 返回标量的 UTF-8 编码长度（非法标量按 FFFD 计）。
func Len(r rune) int {
	if !scalar.IsScalar(r) {
		r = scalar.RuneError
	}
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
