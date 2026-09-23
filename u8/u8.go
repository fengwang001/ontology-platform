// Package u8 实现 UTF-8 逐标量解码/编码，禁止使用 unicode/utf8。
package u8

import "ontology/scalar"

// Unit 是一次解码结果：合法标量或一个非法单元。
type Unit struct {
	R     scalar.Rune
	Valid bool
	Off   int // 单元在整个输入流中的起始字节偏移
	Len   int // 该单元吞掉的字节数
}

// Decoder 是有状态的 UTF-8 解码器；残留在 need>0 时不产生任何输出。
type Decoder struct {
	need, seen int
	cp         scalar.Rune
	lo, hi     byte // 第二字节特殊合法区间（0 表示无限制，用 has 判定）
	has        bool
	start      int
}

func NewDecoder() *Decoder { return &Decoder{} }

// Span 返回首字节 b 的期望序列长度；非可跨切首字节返回 0。
func Span(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 0
}

func (d *Decoder) emitBad(emit func(Unit), off, n int) {
	emit(Unit{R: scalar.Replacement, Off: off, Len: n})
	d.need, d.seen, d.has = 0, 0, false
}

// startLead 在同一步内把 b 当作新首字节处理（不重复检查）。
func (d *Decoder) startLead(b byte, off int, emit func(Unit)) {
	d.start, d.seen, d.has = off, 0, false
	switch {
	case b < 0x80:
		emit(Unit{R: scalar.Rune(b), Valid: true, Off: off, Len: 1})
		d.need = 0
	case b >= 0xC2 && b <= 0xDF:
		d.need, d.cp = 2, scalar.Rune(b&0x1F)
	case b == 0xE0:
		d.need, d.cp, d.has, d.lo, d.hi = 3, scalar.Rune(b&0x0F), true, 0xA0, 0xBF
	case b >= 0xE1 && b <= 0xEC:
		d.need, d.cp = 3, scalar.Rune(b&0x0F)
	case b == 0xED:
		d.need, d.cp, d.has, d.lo, d.hi = 3, scalar.Rune(b&0x0F), true, 0x80, 0x9F
	case b >= 0xEE && b <= 0xEF:
		d.need, d.cp = 3, scalar.Rune(b&0x0F)
	case b == 0xF0:
		d.need, d.cp, d.has, d.lo, d.hi = 4, scalar.Rune(b&0x07), true, 0x90, 0xBF
	case b >= 0xF1 && b <= 0xF3:
		d.need, d.cp = 4, scalar.Rune(b&0x07)
	case b == 0xF4:
		d.need, d.cp, d.has, d.lo, d.hi = 4, scalar.Rune(b&0x07), true, 0x80, 0x8F
	default: // 80..BF、C0 C1 F5..FF
		emit(Unit{R: scalar.Replacement, Off: off, Len: 1})
		d.need = 0
	}
}

// Feed 喂入一段字节，base 为 p[0] 的绝对偏移；每字节恰好检查一次。
func (d *Decoder) Feed(p []byte, base int, emit func(Unit)) {
	for i, b := range p {
		off := base + i
		if d.need == 0 {
			d.startLead(b, off, emit)
			continue
		}
		if d.seen == 0 && d.has && !scalar.InRange(b, d.lo, d.hi) {
			if scalar.IsCont(b) { // 续写但越界：吞掉它
				d.emitBad(emit, d.start, 2)
			} else { // 新首字节：不吞，同一步转作新首字节
				d.emitBad(emit, d.start, 1)
				d.startLead(b, off, emit)
			}
			continue
		}
		if !scalar.IsCont(b) { // 第三/四字节非续写：不吞，转作新首字节
			d.emitBad(emit, d.start, d.seen+1)
			d.startLead(b, off, emit)
			continue
		}
		d.seen++
		d.cp = d.cp<<6 | scalar.Rune(b&0x3F)
		if d.seen == d.need-1 {
			emit(Unit{R: d.cp, Valid: scalar.IsScalar(d.cp), Off: d.start, Len: d.need})
			d.need, d.seen, d.has = 0, 0, false
		}
	}
}

// Flush 处理流结束残留；有残留时返回其起始偏移与已吞字节数（seen+1）。
func (d *Decoder) Flush() (off, n int, truncated bool) {
	if d.need == 0 {
		return 0, 0, false
	}
	off, n, truncated = d.start, d.seen+1, true
	d.need, d.seen, d.has = 0, 0, false
	return
}

// Encode 把合法标量追加编码到 dst。
func Encode(dst []byte, r scalar.Rune) []byte {
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

// EncLen 返回 r 的 UTF-8 编码字节数。
func EncLen(r scalar.Rune) int {
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
