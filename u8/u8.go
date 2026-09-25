// Package u8 手写 UTF-8 逐字节解码/编码，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// MaxPending 是未完成前缀缓存硬上限（最长 4 字节序列，含首字节）。
const MaxPending = 4

// 单元种类。
const (
	KindNone    = 0 // 仍在累积
	KindScalar  = 1 // 合法标量
	KindIllegal = 2 // 一个非法单元
)

// Decoder 是跨 Write 保持状态的逐字节解码器。
type Decoder struct {
	need, got   int
	second      bool
	lo2, hi2    byte
	r           rune
	pending     []byte
	started     bool
}

// Pending 返回挂起的未完成原始前缀字节（调用方可保留副本）。
func (d *Decoder) Pending() []byte { return d.pending }

// Close 报告流结束时是否残留未完成前缀；残留时返回其字节数。
func (d *Decoder) Close() int {
	if d.need != 0 {
		return len(d.pending)
	}
	return 0
}

// Reset 清空状态。
func (d *Decoder) Reset() {
	d.need, d.got, d.second = 0, 0, false
	d.r, d.started = 0, false
	d.pending = d.pending[:0]
}

// Feed 喂入一个字节。返回种类、标量、本单元吞掉的字节数；
// repOK 为真时 rep 必须作为下一字节重新喂入（坏字节重解析），且不另计检查。
func (d *Decoder) Feed(b byte, checks *int) (kind int, r rune, n int, rep byte, repOK bool) {
	if checks != nil {
		*checks++
	}
	if d.need == 0 {
		if b < 0x80 {
			return KindScalar, rune(b), 1, 0, false
		}
		if b >= 0xC2 && b <= 0xF4 {
			d.start(b)
			d.pending = append(d.pending[:0], b)
			return KindNone, 0, 0, 0, false
		}
		return KindIllegal, 0, 1, 0, false // 80..BF / C0 / C1 / F5..FF
	}
	d.got++
	ok := scalar.Cont(b)
	if d.second {
		ok = ok && b >= d.lo2 && b <= d.hi2
		d.second = false
	}
	if !ok {
		nn := len(d.pending)
		d.Reset()
		return KindIllegal, 0, nn, b, true
	}
	d.r = d.r<<6 | rune(b&0x3F)
	d.pending = append(d.pending, b)
	if d.got < d.need {
		return KindNone, 0, 0, 0, false
	}
	rr, nn := d.r, len(d.pending)
	d.Reset()
	return KindScalar, rr, nn, 0, false
}

func (d *Decoder) start(b byte) {
	d.started = true
	switch {
	case b < 0xE0: // C2..DF
		d.need, d.r, d.lo2, d.hi2 = 1, rune(b&0x1F), 0x80, 0xBF
	case b == 0xE0:
		d.need, d.r, d.lo2, d.hi2 = 2, 0, 0xA0, 0xBF
	case b < 0xED: // E1..EC
		d.need, d.r, d.lo2, d.hi2 = 2, rune(b&0x0F), 0x80, 0xBF
	case b == 0xED:
		d.need, d.r, d.lo2, d.hi2 = 2, 0x0D, 0x80, 0x9F
	case b < 0xF0: // EE..EF
		d.need, d.r, d.lo2, d.hi2 = 2, rune(b&0x0F), 0x80, 0xBF
	case b == 0xF0:
		d.need, d.r, d.lo2, d.hi2 = 3, 0, 0x90, 0xBF
	case b < 0xF4: // F1..F3
		d.need, d.r, d.lo2, d.hi2 = 3, rune(b&0x07), 0x80, 0xBF
	default: // F4
		d.need, d.r, d.lo2, d.hi2 = 3, 0, 0x80, 0x8F
	}
	d.got, d.second = 0, true
}

// Encode 把一个合法标量编码为 UTF-8。
func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte((r>>6)&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte((r>>12)&0x3F),
			0x80 | byte((r>>6)&0x3F), 0x80 | byte(r&0x3F)}
	}
}

// unit 从 off 起无状态解析一个单元。end 为下一单元起点；incomplete 表示到尾仍是合法前缀。
func unit(p []byte, off int) (kind int, r rune, end int, incomplete bool) {
	var d Decoder
	start := off
	for off < len(p) {
		k, rr, _, rep, repOK := d.Feed(p[off], nil)
		if repOK {
			return KindIllegal, 0, off, false // [start,off) 为前缀，坏字节在 off 重解析
		}
		off++
		if k == KindScalar {
			return KindScalar, rr, off, false
		}
		if k == KindIllegal {
			return KindIllegal, 0, off, false
		}
	}
	if d.Close() != 0 {
		return KindNone, 0, len(p), true
	}
	return KindNone, 0, start, false
}

// Align 返回在 cut 处切分时，解析应真正回退到的起点（回看不超过 3 字节）。
func Align(p []byte, cut int) int {
	off := cut - 3
	if off < 0 {
		off = 0
	}
	for off < cut {
		start := off
		_, _, end, incomplete := unit(p, off)
		if incomplete {
			return start
		}
		if end >= cut {
			if end == cut {
				return cut
			}
			return start
		}
		off = end
	}
	return cut
}
