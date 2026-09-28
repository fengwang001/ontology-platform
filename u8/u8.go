// Package u8 手写 UTF-8 字节级解码/编码（不使用 unicode/utf8 与隐式解码）。
package u8

import "ontology/scalar"

// Kind 是解码事件类别。
type Kind uint8

const (
	KRune Kind = iota // 合法标量
	KBad              // 一个非法单元（长度见 Len）
)

// Event 描述一次解码结果：标量或一个非法单元。
type Event struct {
	Kind Kind
	R    rune
	Off  int // 单元在本次喂入拼接流中的起始字节偏移
	Len  int // 单元字节长度
}

// Decoder 是跨 Write 复用的 UTF-8 状态机；每个字节恰好检查一次。
type Decoder struct {
	car      [3]byte // 未闭合的合法前缀
	nc       int     // car 内字节数（0..3）
	need     int     // 还需续字节数
	r        rune    // 已累加码点
	pstart   int     // 单元起始在当前拼接缓冲中的偏移
	Examined int     // 被检查的总字节数（非导出计数的导出只读口）
}

// Pending 返回当前缓存的字节数（硬上限 3）。
func (d *Decoder) Pending() int { return d.nc }

func cont(b byte) (rune, bool) { return rune(b & 0x3F), b&0xC0 == 0x80 }

// Feed 追加 p 并对每个完整单元调用 cb。offBase 为 p[0] 在整体输入流的偏移。
func (d *Decoder) Feed(p []byte, offBase int, cb func(Event)) {
	for _, b := range p {
		d.Examined++
		off := offBase
		offBase++
		d.ingest(b, off, cb)
	}
}

func (d *Decoder) ingest(b byte, off int, cb func(Event)) {
	switch {
	case d.nc == 0 && b < 0x80:
		cb(Event{KRune, rune(b), off, 1})
	case d.nc == 0 && b >= 0xC2 && b <= 0xDF:
		d.car[0], d.nc, d.need, d.r = b, 1, 1, rune(b&0x1F)
	case d.nc == 0 && b == 0xE0:
		d.car[0], d.nc, d.need, d.r = b, 1, 2, 0
	case d.nc == 0 && b >= 0xE1 && b <= 0xEC:
		d.car[0], d.nc, d.need, d.r = b, 1, 2, rune(b&0x0F)
	case d.nc == 0 && b == 0xED:
		d.car[0], d.nc, d.need, d.r = b, 1, 2, 0x0D
	case d.nc == 0 && b >= 0xEE && b <= 0xEF:
		d.car[0], d.nc, d.need, d.r = b, 1, 2, rune(b&0x0F)
	case d.nc == 0 && b == 0xF0:
		d.car[0], d.nc, d.need, d.r = b, 1, 3, 0
	case d.nc == 0 && b >= 0xF1 && b <= 0xF3:
		d.car[0], d.nc, d.need, d.r = b, 1, 3, rune(b&0x07)
	case d.nc == 0 && b == 0xF4:
		d.car[0], d.nc, d.need, d.r = b, 1, 3, 4
	case d.nc == 0:
		cb(Event{KBad, scalar.Replacement, off, 1}) // 80..BF / C0 C1 F5..FF
	default:
		d.stepCont(b, off, cb)
	}
}

func (d *Decoder) stepCont(b byte, off int, cb func(Event)) {
	first := d.car[0]
	ok := b&0xC0 == 0x80
	if ok && d.nc == 1 { // 第二字节有专门区间
		switch first {
		case 0xE0:
			ok = b >= 0xA0
		case 0xED:
			ok = b <= 0x9F
		case 0xF0:
			ok = b >= 0x90
		case 0xF4:
			ok = b <= 0x8F
		}
	}
	if !ok { // 当前前缀整体是一个非法单元；坏字节留在原位置重解析
		cb(Event{KBad, scalar.Replacement, off - d.nc, d.nc})
		d.nc, d.need = 0, 0
		d.ingest(b, off, cb)
		return
	}
	d.r = d.r<<6 | rune(b&0x3F)
	d.need--
	if d.need == 0 {
		cb(Event{KRune, d.r, off - d.nc, d.nc + 1})
		d.nc = 0
		return
	}
	d.car[d.nc] = b
	d.nc++
}

// Flush 在流结束时报告残留前缀：替换模式为 1 个非法单元。
func (d *Decoder) Flush(offBase int, cb func(Event)) {
	if d.nc > 0 {
		cb(Event{KBad, scalar.Replacement, offBase - d.nc, d.nc})
		d.nc, d.need = 0, 0
	}
}

// EncodeRune 把标量编码为 UTF-8 字节；返回 nil 当 r 非标量。
func EncodeRune(r rune) []byte {
	switch {
	case !scalar.IsScalar(r):
		return nil
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
