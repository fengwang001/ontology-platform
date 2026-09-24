// Package u8 实现 UTF-8 字节序列的逐标量解码与编码。
// 非法单元切分规则见 DESIGN.md 第 1 节（与 WHATWG UTF-8 decoder 一致）。
package u8

import "ontology/scalar"

// Event 是解码器产出的一个单元：合法标量或一个非法单元。
type Event struct {
	Scalar int32 // 合法标量值；非法单元时为 scalar.Replacement
	Size   int   // 该单元吞掉的输入字节数
	Valid  bool
}

// Decoder 是增量 UTF-8 解码器，一次喂一个字节。
// 越界字节不被吞掉，而是放入重放槽，由 TakeReplay 取回重新处理。
type Decoder struct {
	need   int // 当前序列总长度，0 表示空闲
	have   int // 已消费字节数
	lo, hi byte
	cp     int32
	replay [1]byte
	hasRe  bool
}

// Reset 清空全部状态。
func (d *Decoder) Reset() { *d = Decoder{} }

// Idle 报告当前是否没有未完成的序列。
func (d *Decoder) Idle() bool { return d.need == 0 && !d.hasRe }

// Pending 报告缓存的未决字节数（硬上限 4，见 DESIGN.md 第 2 节）。
func (d *Decoder) Pending() int {
	n := d.have
	if d.hasRe {
		n++
	}
	return n
}

// HasReplay 报告是否有待重放的越界字节。
func (d *Decoder) HasReplay() bool { return d.hasRe }

// TakeReplay 取出越界字节；调用方须随后将其重新 Feed。
func (d *Decoder) TakeReplay() (byte, bool) {
	if !d.hasRe {
		return 0, false
	}
	d.hasRe = false
	return d.replay[0], true
}

// Feed 处理一个字节；ok 为 true 时产出了一个完整单元。
func (d *Decoder) Feed(b byte) (ev Event, ok bool) {
	if d.need == 0 {
		switch {
		case b < 0x80:
			return Event{int32(b), 1, true}, true
		case b >= 0xC2 && b <= 0xDF:
			d.need, d.have, d.lo, d.hi, d.cp = 2, 1, 0x80, 0xBF, int32(b&0x1F)
		case b >= 0xE0 && b <= 0xEF:
			d.need, d.have, d.lo, d.hi, d.cp = 3, 1, 0x80, 0xBF, int32(b&0x0F)
			if b == 0xE0 {
				d.lo = 0xA0
			} else if b == 0xED {
				d.hi = 0x9F
			}
		case b >= 0xF0 && b <= 0xF4:
			d.need, d.have, d.lo, d.hi, d.cp = 4, 1, 0x80, 0xBF, int32(b&0x07)
			if b == 0xF0 {
				d.lo = 0x90
			} else if b == 0xF4 {
				d.hi = 0x8F
			}
		default: // 80..BF、C0 C1、F5..FF：非法首字节，单元长度 1
			return Event{scalar.Replacement, 1, false}, true
		}
		return Event{}, false
	}
	if b < d.lo || b > d.hi { // 越界：前缀成单元，越界字节重放
		ev = Event{scalar.Replacement, d.have, false}
		d.need, d.have = 0, 0
		d.replay[0], d.hasRe = b, true
		return ev, true
	}
	d.cp = d.cp<<6 | int32(b&0x3F)
	d.have++
	d.lo, d.hi = 0x80, 0xBF
	if d.have == d.need {
		ev = Event{d.cp, d.need, true}
		d.need, d.have = 0, 0
		return ev, true
	}
	return Event{}, false
}

// Finish 在流结束时冲刷：未完成的合法前缀整体成为一个非法单元。
func (d *Decoder) Finish() (Event, bool) {
	if d.need == 0 {
		return Event{}, false
	}
	ev := Event{scalar.Replacement, d.have, false}
	d.need, d.have = 0, 0
	return ev, true
}

// EncodedLen 返回合法标量 cp 的 UTF-8 编码长度。
func EncodedLen(cp int32) int {
	switch {
	case cp < 0x80:
		return 1
	case cp < 0x800:
		return 2
	case cp < 0x10000:
		return 3
	default:
		return 4
	}
}

// AppendEncode 把合法标量 cp 的 UTF-8 编码追加到 dst。
func AppendEncode(dst []byte, cp int32) []byte {
	switch EncodedLen(cp) {
	case 1:
		return append(dst, byte(cp))
	case 2:
		return append(dst, 0xC0|byte(cp>>6), 0x80|byte(cp&0x3F))
	case 3:
		return append(dst, 0xE0|byte(cp>>12), 0x80|byte(cp>>6&0x3F), 0x80|byte(cp&0x3F))
	default:
		return append(dst, 0xF0|byte(cp>>18), 0x80|byte(cp>>12&0x3F), 0x80|byte(cp>>6&0x3F), 0x80|byte(cp&0x3F))
	}
}

// AlignEnd 返回 >= s 的最小单元边界（par 切点对齐用，见 DESIGN.md 第 3 节）。
// 从 s 前最多 3 个候选起点局部解码；任一候选解出跨过 s 的单元即以其结束位置为准。
func AlignEnd(data []byte, s int) int {
	if s <= 0 || s >= len(data) {
		return s
	}
	lo := s - 3
	if lo < 0 {
		lo = 0
	}
	for a := s - 1; a >= lo; a-- {
		var d Decoder
		i := a
		for {
			if !d.HasReplay() && i >= s {
				if d.Idle() {
					break // 相对边界，继续试更早的起点
				}
				// 未决前缀跨过 s：解完它找结束位置
				for !d.Idle() && i < len(data) {
					if rb, ok := d.TakeReplay(); ok {
						d.Feed(rb)
					} else {
						d.Feed(data[i])
						i++
					}
				}
				return i // 单元结束位置（截断时即 len(data)）
			}
			var b byte
			if rb, ok := d.TakeReplay(); ok {
				b = rb
			} else {
				b = data[i]
				i++
			}
			d.Feed(b)
		}
	}
	return s
}
