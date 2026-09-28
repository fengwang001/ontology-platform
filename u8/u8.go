// Package u8 在字节级实现 UTF-8 逐标量解码与编码，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// BOM 是 UTF-8 字节序标记 U+FEFF 的编码。
var BOM = []byte{0xEF, 0xBB, 0xBF}

// Event 是单字节推进后的事件类型。
type Event uint8

const (
	NeedMore Event = iota // 已缓存，等待后续字节
	Scalar                // 完整标量
	Invalid               // 一个非法单元结束
)

// Result 描述 Push 的结果。
type Result struct {
	Event     Event
	R         rune // Event==Scalar 时的标量值
	Consumed  int  // 该事件在输入中吞掉的字节数
	Reprocess bool // 触发判定的字节未被吞，需重新喂入
}

// Decoder 是跨 Write 的 UTF-8 增量状态机，单实例非并发安全。
type Decoder struct {
	pend [4]byte
	n    int
}

// Pending 返回当前缓存的合法前缀字节数（0..3）。
func (d *Decoder) Pending() int { return d.n }

// PendingBytes 把当前缓存复制进 b（b 长度不足时截断），返回复制字节数。
func (d *Decoder) PendingBytes(b []byte) int {
	return copy(b, d.pend[:d.n])
}

// Push 推进一个字节，每个字节只检查一次。
func (d *Decoder) Push(b byte) Result {
	if d.n == 0 {
		switch {
		case b < 0x80:
			return Result{Event: Scalar, R: rune(b), Consumed: 1}
		case scalar.SeqLen(b) == 0:
			return Result{Event: Invalid, Consumed: 1}
		}
		d.pend[0] = b
		d.n = 1
		return Result{Event: NeedMore}
	}
	lead := d.pend[0]
	ok := false
	if d.n == 1 {
		ok = scalar.SecondByteOK(lead, b)
	} else {
		ok = scalar.IsCont(b)
	}
	if !ok {
		c := d.n
		d.n = 0
		return Result{Event: Invalid, Consumed: c, Reprocess: true}
	}
	d.pend[d.n] = b
	d.n++
	if d.n != scalar.SeqLen(lead) {
		return Result{Event: NeedMore}
	}
	r := decode(d.pend[:d.n])
	d.n = 0
	return Result{Event: Scalar, R: r, Consumed: scalar.SeqLen(lead)}
}

// Flush 在流结束时调用：残留真前缀作为一个单元（截断/替换）。
func (d *Decoder) Flush() (consumed int, ok bool) {
	if d.n == 0 {
		return 0, true
	}
	c := d.n
	d.n = 0
	return c, false
}

func decode(p []byte) rune {
	var r rune
	switch len(p) {
	case 2:
		r = rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		r = rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	case 4:
		r = rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 |
			rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
	return r
}

// Encode 把标量编码为 UTF-8，目标必须是合法标量。
func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F),
			0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	}
}

// Reset 清空状态机。
func (d *Decoder) Reset() { d.n = 0 }
