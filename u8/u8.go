// Package u8 在字节级实现 UTF-8 逐标量解码与编码（不使用 unicode/utf8）。
package u8

import "ontology/scalar"

// Replacement 是替换字符 U+FFFD。
const Replacement rune = 0xFFFD

// PendingCap 是任意时刻待解析字节的硬上限。
const PendingCap = 3

// Event 是每消费一个单元（合法标量或非法单元）后产生的事件。
type Event struct {
	R    rune // 合法时为标量；非法时为 Replacement
	OK   bool // 合法标量
	Len  int  // 本单元在输入流中吞掉的字节数
	From int // Len 中来自新喂入数据（而非上次 pending）的字节数
}

// Decoder 是有状态 UTF-8 解码器，单实例非并发安全。
type Decoder struct {
	pend  [PendingCap]byte
	npend int // pending 字节数（首字节+已接受的续字节）
	cp    rune
	need  int // 还需几个续字节
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

// SecondOK 按首字节报告第二字节 b 是否落在合法窗口内。
func SecondOK(lead, b byte) bool {
	switch lead {
	case 0xE0:
		return b >= 0xA0 && b <= 0xBF
	case 0xED:
		return b >= 0x80 && b <= 0x9F
	case 0xF0:
		return b >= 0x90 && b <= 0xBF
	case 0xF4:
		return b >= 0x80 && b <= 0x8F
	default:
		return cont(b)
	}
}

func leadLen(b byte) int {
	switch {
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}

// Feed 喂入数据，返回事件数 evs[:k] 与未消费（留给下次）的输入尾部。
// 每个输入字节至多被检查两次；From 仅计本单元在本次喂入中的字节数。
func (d *Decoder) Feed(p []byte, evs []Event, checks *int64) (k int, rest []byte) {
	rest = p
	fromN := 0
	for len(rest) > 0 && k < len(evs) {
		b := rest[0]
		*checks++
		if d.need > 0 {
			good := cont(b)
			if d.npend == 1 {
				good = SecondOK(d.pend[0], b)
			}
			if good {
				d.cp = d.cp<<6 | rune(b&0x3F)
				d.pend[d.npend] = b
				d.npend++
				fromN++
				d.need--
				rest = rest[1:]
				if d.need > 0 {
					continue
				}
				ok := scalar.IsScalar(d.cp)
				r := d.cp
				if !ok {
					r = Replacement
				}
				evs[k] = Event{R: r, OK: ok, Len: d.npend, From: fromN}
				k++
				d.npend, d.need = 0, 0
				fromN = 0
				continue
			}
			evs[k] = Event{R: Replacement, Len: d.npend, From: fromN}
			k++
			d.npend, d.need = 0, 0
			fromN = 0
			continue // b 未消费：以 ground 重判
		}
		rest = rest[1:]
		switch {
		case b < 0x80:
			evs[k] = Event{R: rune(b), OK: true, Len: 1, From: 1}
			k++
		case cont(b), b == 0xC0, b == 0xC1, b > 0xF4:
			evs[k] = Event{R: Replacement, Len: 1, From: 1}
			k++
		default:
			d.pend[0] = b
			d.npend = 1
			d.need = leadLen(b) - 1
			fromN = 1
			switch {
			case b < 0xE0:
				d.cp = rune(b & 0x1F)
			case b < 0xF0:
				d.cp = rune(b & 0x0F)
			default:
				d.cp = rune(b & 0x07)
			}
		}
	}
	return k, rest
}

// PendingLen 返回当前残留前缀字节数。
func (d *Decoder) PendingLen() int { return d.npend }

// Flush 在流结束时报告残留前缀：nil 表示无残留。
func (d *Decoder) Flush() *Event {
	if d.npend == 0 {
		return nil
	}
	e := Event{R: Replacement, Len: d.npend, From: d.npend}
	d.npend, d.need = 0, 0
	return &e
}

// Encode 把单个标量编码为 UTF-8；非 scalar 返回 false。
func Encode(r rune) ([]byte, bool) {
	if !scalar.IsScalar(r) {
		return nil, false
	}
	switch {
	case r < 0x80:
		return []byte{byte(r)}, true
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}, true
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}, true
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}, true
	}
}
