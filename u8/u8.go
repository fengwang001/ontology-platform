// Package u8 在字节级实现 UTF-8 的流式解码与编码，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// Event 是每步解码结果的类别。
type Event uint8

const (
	// None 表示尚无完整事件（字节被缓存为未完成前缀）。
	None Event = iota
	// Rune 表示产出一个合法标量。
	Rune
	// Bad 表示一个非法单元定稿。
	Bad
)

// Decoder 是逐字节增量 UTF-8 解码器；零值即可用。
type Decoder struct {
	need  int  // 还需续字节数
	total int  // 当前序列总长度
	min   byte // 第二字节下界
	max   byte // 第二字节上界
	r     rune // 已累计的值
}

// Step 喂入一个字节 b。返回事件、标量值（仅 Rune 有效）与本字节是否被消费。
// 当未完成前缀因 b 而定稿为非法单元时返回 (Bad,_,false)：b 未消费，须重新喂入。
func (d *Decoder) Step(b byte) (Event, rune, bool) {
	if d.need > 0 {
		lo, hi := byte(0x80), byte(0xBF)
		if d.need == d.total-1 { // b 正是第二字节
			lo, hi = d.min, d.max
		}
		if b >= lo && b <= hi {
			d.r = d.r<<6 | rune(b&0x3F)
			d.need--
			if d.need == 0 {
				r := d.r
				d.reset()
				return Rune, r, true
			}
			return None, 0, true
		}
		d.reset()
		return Bad, 0, false // 极大子部分定稿，b 退回
	}
	switch {
	case b < 0x80:
		return Rune, rune(b), true
	case b < 0xC2: // 80..BF 续字节, C0 C1
		return Bad, 0, true
	case b < 0xE0:
		d.start(b, 2, 0x80, 0xBF)
	case b == 0xE0:
		d.start(b, 3, 0xA0, 0xBF)
	case b < 0xED:
		d.start(b, 3, 0x80, 0xBF)
	case b == 0xED:
		d.start(b, 3, 0x80, 0x9F)
	case b < 0xF0:
		d.start(b, 3, 0x80, 0xBF)
	case b == 0xF0:
		d.start(b, 4, 0x90, 0xBF)
	case b < 0xF4:
		d.start(b, 4, 0x80, 0xBF)
	case b == 0xF4:
		d.start(b, 4, 0x80, 0x8F)
	default: // F5..FF
		return Bad, 0, true
	}
	return None, 0, true
}

func (d *Decoder) start(b byte, total int, lo, hi byte) {
	d.total, d.need, d.min, d.max = total, total-1, lo, hi
	d.r = rune(b & (0xFF >> uint(total)))
}

func (d *Decoder) reset() { d.need, d.total, d.r = 0, 0, 0 }

// Pending 报告是否缓存着未完成前缀及其字节数（1..3）。
func (d *Decoder) Pending() int {
	if d.need == 0 {
		return 0
	}
	return d.total - d.need
}

// Flush 在流结束时定稿缓存：无挂起返回 None；否则返回 Bad（截断）。
func (d *Decoder) Flush() Event {
	if d.need == 0 {
		return None
	}
	d.reset()
	return Bad
}

// Encode 把合法标量编码为 UTF-8 字节；非法标量返回 nil。
func Encode(r rune) []byte {
	if !scalar.Valid(r) {
		return nil
	}
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F,
			0x80 | byte(r>>6)&0x3F, 0x80 | byte(r&0x3F)}
	}
}

// IsLead 报告 b 是否为合法多字节序列首字节；若是再给期望总长度。
func IsLead(b byte) (bool, int) {
	switch {
	case b >= 0xC2 && b < 0xE0:
		return true, 2
	case b >= 0xE0 && b < 0xF0:
		return true, 3
	case b >= 0xF0 && b < 0xF5:
		return true, 4
	}
	return false, 0
}

// Handback 返回切点前应交给下一段的未决子部分长度（回看 at 的末尾，至多 3）。
// 直接用解码器模拟，确保第二字节区间违规（如 E0 80）不会被误交。
func Handback(at []byte) int {
	for i := len(at) - 1; i >= 0 && i >= len(at)-3; i-- {
		ok, _ := IsLead(at[i])
		if !ok {
			continue
		}
		var d Decoder
		for _, b := range at[i:] {
			ev, _, _ := d.Step(b)
			if ev != None { // 已产出（合法或非法定稿），切点不在此序列中间
				d.reset()
				break
			}
		}
		if p := d.Pending(); p > 0 {
			return p
		}
	}
	return 0
}
