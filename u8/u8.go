// Package u8 手写字节级 UTF-8 解码/编码，不使用 unicode/utf8 或隐式字符串解码。
package u8

import "ontology/scalar"

const MaxPending = 4 // 切分缓存硬上限（最长字符 4 字节）

// Decoder 是逐字节状态机：每次只前进一个字节，绝不回扫已确认的前缀。
type Decoder struct {
	lead            byte
	r               rune
	n, got          int
	lo2, hi2        byte // 第二字节合法区间
	mid             bool
	Checks          int64
}

func (d *Decoder) Reset() { *d = Decoder{} }

// Pending 返回缓存中尚未成单元的字节数（恒 < MaxPending）。
func (d *Decoder) Pending() int {
	if d.mid {
		return d.got
	}
	return 0
}

// Push 喂入一个字节。单元完成时 size>0；invalid 为真表示非法单元（r 无意义）；
// reprocess 为真时调用方必须把同一个 b 再 Push 一次（它不属于刚关闭的前缀）。
func (d *Decoder) Push(b byte) (size int, r rune, invalid, reprocess bool) {
	d.Checks++
	if !d.mid {
		switch {
		case b < 0x80:
			return 1, rune(b), false, false
		case b < 0xC2: // 80..BF 游离延续，C0 C1 永不合法
			return 1, 0, true, false
		case b < 0xE0:
			d.start(b, 2, 0x80, 0xBF, rune(b&0x1F))
		case b == 0xE0:
			d.start(b, 3, 0xA0, 0xBF, rune(b&0x0F))
		case b < 0xED:
			d.start(b, 3, 0x80, 0xBF, rune(b&0x0F))
		case b == 0xED:
			d.start(b, 3, 0x80, 0x9F, rune(b&0x0F))
		case b < 0xF0:
			d.start(b, 3, 0x80, 0xBF, rune(b&0x0F))
		case b == 0xF0:
			d.start(b, 4, 0x90, 0xBF, rune(b&0x07))
		case b < 0xF4:
			d.start(b, 4, 0x80, 0xBF, rune(b&0x07))
		case b == 0xF4:
			d.start(b, 4, 0x80, 0x8F, rune(b&0x07))
		default: // F5..FF
			return 1, 0, true, false
		}
		return 0, 0, false, false
	}
	lo, hi := byte(0x80), byte(0xBF)
	if d.got == 1 {
		lo, hi = d.lo2, d.hi2
	}
	if b < lo || b > hi {
		size, d.mid = d.got, false
		return size, 0, true, true // 前缀整体非法；b 重新解析
	}
	d.r = d.r<<6 | rune(b&0x3F)
	d.got++
	if d.got < d.n {
		return 0, 0, false, false
	}
	r, d.mid = d.r, false
	return d.n, r, false, false
}

func (d *Decoder) start(lead byte, n int, lo2, hi2 byte, r rune) {
	d.lead, d.n, d.lo2, d.hi2, d.r, d.got, d.mid = lead, n, lo2, hi2, r, 1, true
}

// Finish 在流结束/段边界调用：mid 时返回残留前缀长度与 invalid=true。
func (d *Decoder) Finish() (size int, invalid bool) {
	if !d.mid {
		return 0, false
	}
	size, d.mid = d.got, false
	return size, true
}

// SegmentStart 返回从 cut 切分后，后一段真正应开始解析的字节偏移
// （回看 ≤3 字节；若 cut 落在某个单元内部则回退到该单元首字节）。
func SegmentStart(p []byte, cut int) int {
	if cut <= 0 || cut >= len(p) {
		return cut
	}
	i := cut - 1
	if p[i] < 0x80 || p[i] > 0xBF {
		if p[i] >= 0xC2 && p[i] <= 0xF4 {
			return i // 引导符后可能还跟延续字节
		}
		return cut
	}
	for i > 0 && p[i-1] >= 0x80 && p[i-1] <= 0xBF && cut-i < 3 {
		i--
	}
	if i == 0 || p[i-1] < 0xC2 || p[i-1] > 0xF4 {
		return cut
	}
	lead := p[i-1]
	n := 2
	if lead >= 0xE0 {
		n = 3
	}
	if lead >= 0xF0 {
		n = 4
	}
	if cut-i+1 > n-1 {
		return cut // 延续链过长，末字节是游离单元
	}
	lo, hi := byte(0x80), byte(0xBF)
	switch lead {
	case 0xE0:
		lo, hi = 0xA0, 0xBF
	case 0xED:
		lo, hi = 0x80, 0x9F
	case 0xF0:
		lo, hi = 0x90, 0xBF
	case 0xF4:
		lo, hi = 0x80, 0x8F
	}
	if p[i] < lo || p[i] > hi {
		return cut // 第二字节非法：引导符自成 1 字节单元，延续字节游离
	}
	return i - 1
}

// Len 返回标量的 UTF-8 编码长度，非法标量返回 0。
func Len(r rune) int {
	switch {
	case !scalar.IsScalar(r):
		return 0
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

// Encode 把标量追加到 dst（调用方保证 r 合法）。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
