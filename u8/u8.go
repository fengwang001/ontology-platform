// Package u8 在字节级实现 UTF-8 的逐标量解码与编码（不使用 unicode/utf8）。
package u8

import "ontology/scalar"

// Event 是一次解码结果：要么 Rune 合法（Bad=false），要么 Bad=true 表示
// 一个非法单元，Len 为该单元吞掉的字节数。
type Event struct {
	Rune rune
	Len  int
	Bad  bool
}

// Decoder 是字节驱动的流式 UTF-8 解码器。
type Decoder struct {
	buf   [4]byte
	n     int
	want  int
	queue Event
	hasQ  bool
	check uint64
}

// Checks 返回自重置以来字节被检查的总次数。
func (d *Decoder) Checks() uint64 { return d.check }

// PendingLen 返回当前未完成前缀占用的字节数（≤4）。
func (d *Decoder) PendingLen() int { return d.n }

// Reset 清空解码器状态（不清零 Checks 以便累计）。
func (d *Decoder) Reset() { d.n, d.want, d.hasQ = 0, 0, false }

func seqLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xC2:
		return 0 // 续字节或 C0/C1：非法首字节
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	case b < 0xF5:
		return 4
	default:
		return 0
}

func secondOK(lead, b2 byte) bool {
	switch {
	case lead == 0xE0:
		return b2 >= 0xA0
	case lead == 0xED:
		return b2 <= 0x9F
	case lead == 0xF0:
		return b2 >= 0x90
	case lead == 0xF4:
		return b2 <= 0x8F
	default:
		return true
	}
}

// Push 喂入一个字节；事件完整时返回 Event 和 true，前缀未完成时 ok=false。
// 当一个字节既终止旧的非法前缀、自身又构成新单元时，第二事件进入队列，
// 用 Queued 取出。
func (d *Decoder) Push(b byte) (Event, bool) {
	d.check++
	if d.n == 0 {
		k := seqLen(b)
		if k == 0 {
			return Event{Rune: scalar.Replacement, Len: 1, Bad: true}, true
		}
		d.buf[0], d.n, d.want = b, 1, k
		if k == 1 {
			d.n = 0
			return Event{Rune: rune(b), Len: 1}, true
		}
		return Event{}, false
	}
	lead := d.buf[0]
	if b < 0x80 || b > 0xBF || (d.n == 1 && !secondOK(lead, b)) {
		ev := d.flushBad()
		d.queue, d.hasQ = d.stepFresh(b)
		return ev, true
	}
	d.buf[d.n] = b
	d.n++
	if d.n < d.want {
		return Event{}, false
	}
	r := decodeSeq(d.buf[:d.want])
	l := d.n
	d.n, d.want = 0, 0
	return Event{Rune: r, Len: l}, true
}

// Queued 取出并清空排队事件（终止字节自身构成单元时产生）。
func (d *Decoder) Queued() (Event, bool) {
	if !d.hasQ {
		return Event{}, false
	}
	ev := d.queue
	d.hasQ = false
	return ev, true
}

// stepFresh 把字节当作全新首字节处理（不增加检查计数）。
func (d *Decoder) stepFresh(b byte) (Event, bool) {
	k := seqLen(b)
	if k == 0 {
		return Event{Rune: scalar.Replacement, Len: 1, Bad: true}, true
	}
	if k == 1 {
		return Event{Rune: rune(b), Len: 1}, true
	}
	d.buf[0], d.n, d.want = b, 1, k
	return Event{}, false
}

// Finish 在流结束时调用：残留合法前缀作为一个非法单元返回。
func (d *Decoder) Finish() (Event, bool) {
	if d.n == 0 {
		return Event{}, false
	}
	return d.flushBad(), true
}

func (d *Decoder) flushBad() Event {
	ev := Event{Rune: scalar.Replacement, Len: d.n, Bad: true}
	d.n, d.want = 0, 0
	return ev
}

func decodeSeq(p []byte) rune {
	var r rune
	switch len(p) {
	case 2:
		r = rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		r = rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	case 4:
		r = rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 | rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
	return r
}

// Encode 把标量值编码为 UTF-8，追加到 dst。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F),
			0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	}
}
