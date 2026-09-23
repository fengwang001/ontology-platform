// Package u8 用纯字节判定实现 UTF-8 逐标量解码与编码。解码是增量状态机：
// 每喂一个字节得到 EvOK（合法标量）、EvBad（非法单元，长 UnitLen）或
// EvNeed（序列未结束）。非法字节若不属本单元则 Reprocess=true 归还重喂。
// 禁止使用 unicode/utf8。
package u8

// 事件类型。
const (
	EvNeed = iota // 还需更多字节
	EvOK          // Rune 是一个合法标量
	EvBad         // 一个非法单元，长度 UnitLen
)

// Result 是喂入一个字节后的结果。
type Result struct {
	Event     int
	Rune      rune
	UnitLen   int
	Reprocess bool // 当前字节未被消费，需重新喂入
}

// Decoder 是非并发安全的增量解码器。
type Decoder struct {
	state int // 0=起始 1=2字节序列 2=3字节序列 3=4字节序列
	r     rune
	got   int // 当前序列已收字节数（含首字节）
}

func cont(b byte) bool { return b >= 0x80 && b <= 0xBF }

func (d *Decoder) Feed(b byte) Result {
	for {
		switch d.state {
		case 0:
			switch {
			case b < 0x80:
				return Result{Event: EvOK, Rune: rune(b), UnitLen: 1}
			case b >= 0xC2 && b <= 0xDF:
				d.state, d.got, d.r = 1, 1, rune(b&0x1F)
				return Result{Event: EvNeed}
			case b == 0xE0:
				d.state, d.got, d.r = 2, 1, 0
				return Result{Event: EvNeed}
			case b >= 0xE1 && b <= 0xEF:
				d.state, d.got, d.r = 2, 1, rune(b&0x0F)
				return Result{Event: EvNeed}
			case b == 0xF0:
				d.state, d.got, d.r = 3, 1, 0
				return Result{Event: EvNeed}
			case b >= 0xF1 && b <= 0xF3:
				d.state, d.got, d.r = 3, 1, rune(b&0x07)
				return Result{Event: EvNeed}
			case b == 0xF4:
				d.state, d.got, d.r = 3, 1, 4
				return Result{Event: EvNeed}
			default:
				// 80..BF 无序列续字节，或 C0 C1 F5..FF 非法首字节。
				return Result{Event: EvBad, UnitLen: 1}
			}
		case 1:
			if cont(b) {
				d.state = 0
				return Result{Event: EvOK, Rune: d.r<<6 | rune(b&0x3F), UnitLen: 2}
			}
			n := d.got
			d.state, d.r = 0, 0
			return Result{Event: EvBad, UnitLen: n, Reprocess: true}
		default: // 2=3字节序列, 3=4字节序列
			ok := cont(b)
			specialSecond := false
			if d.got == 1 {
				switch d.state {
				case 2:
					if d.r == 0 { // E0：第二字节必须 A0..BF
						ok = b >= 0xA0 && b <= 0xBF
						specialSecond = true
					} else if d.r == 0x0D { // ED：第二字节必须 80..9F
						ok = b >= 0x80 && b <= 0x9F
						specialSecond = true
					}
				case 3:
					if d.r == 0 { // F0：第二字节必须 90..BF
						ok = b >= 0x90 && b <= 0xBF
						specialSecond = true
					} else if d.r == 4 { // F4：第二字节必须 80..8F
						ok = b >= 0x80 && b <= 0x8F
						specialSecond = true
					}
				}
			}
			if !ok {
				n := d.got
				d.state, d.r, d.got = 0, 0, 0
				if specialSecond {
					// 第二字节违反 E0/ED/F0/F4 的特殊区间：非法单元只含
					// 已收前缀（首字节序列），当前字节归还重处理。
					return Result{Event: EvBad, UnitLen: n, Reprocess: true}
				}
				// 后续位置非法：当前字节是续字节则吞掉整条；非续字节
				// （如 41）不属于本单元，只吞已收前缀并归还当前字节。
				if cont(b) {
					return Result{Event: EvBad, UnitLen: n + 1}
				}
				return Result{Event: EvBad, UnitLen: n, Reprocess: true}
			}
			d.r = d.r<<6 | rune(b&0x3F)
			d.got++
			if (d.state == 2 && d.got == 3) || (d.state == 3 && d.got == 4) {
				n, r := d.got, d.r
				d.state, d.r, d.got = 0, 0, 0
				return Result{Event: EvOK, Rune: r, UnitLen: n}
			}
			return Result{Event: EvNeed}
		}
	}
}

// PendingLen 返回缓存的合法前缀字节数（跨 Write 的半个字符）。
func (d *Decoder) PendingLen() int {
	if d.state == 0 {
		return 0
	}
	return d.got
}

// Close 报告流结束时是否残留未完成序列。
func (d *Decoder) Close() bool { return d.state != 0 }

// EncodeLen 返回合法标量的 UTF-8 编码字节数。
func EncodeLen(r rune) int {
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

// Encode 把合法标量编码进 buf（长度须 ≥ EncodeLen(r)），返回写入长度。
func Encode(buf []byte, r rune) int {
	switch {
	case r < 0x80:
		buf[0] = byte(r)
	case r < 0x800:
		buf[0] = 0xC0 | byte(r>>6)
		buf[1] = 0x80 | byte(r)&0x3F
	case r < 0x10000:
		buf[0] = 0xE0 | byte(r>>12)
		buf[1] = 0x80 | byte(r>>6)&0x3F
		buf[2] = 0x80 | byte(r)&0x3F
	default:
		buf[0] = 0xF0 | byte(r>>18)
		buf[1] = 0x80 | byte(r>>12)&0x3F
		buf[2] = 0x80 | byte(r>>6)&0x3F
		buf[3] = 0x80 | byte(r)&0x3F
	}
	return EncodeLen(r)
}
