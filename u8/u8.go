// Package u8 逐字节做 UTF-8 解码（自带非法单元判定）与编码。
package u8

import "ontology/scalar"

// Event 是一次锚点结算：合法标量或一个非法单元；N 为吞掉的字节数。
type Event struct {
	R       rune
	Illegal bool
	N       int
}

// Decoder 是增量 UTF-8 解码器；残尾留在内部（至多 3 字节），不回扫。
type Decoder struct {
	pend []byte // 已进入、尚未结算的原始字节（≤3）
	lead byte
	need int // 锚点声明总长
	got  int // 已验证续字节数
	mode uint8
	lo2  byte
	hi2  byte
	rem  int // 待决残尾：声明窗口还剩几个续字节槽
	// Checks 为字节被检查的总次数（每个物理字节恰好 1）。
	Checks int64
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

// Feed 喂入一个字节；同一调用内对重解析字节不再重复计费。
func (d *Decoder) Feed(b byte) []Event {
	d.Checks++
	d.pend = append(d.pend, b)
	return d.step(b)
}

// settle 从残尾丢掉 drop 字节结算一个锚点；可选重放某个字节。
func (d *Decoder) settle(r rune, illegal bool, n, drop int, replay byte, doReplay bool) []Event {
	d.pend = d.pend[drop:]
	d.mode, d.need, d.got, d.lo2, d.hi2, d.rem = 0, 0, 0, 0, 0, 0
	evs := []Event{{R: r, Illegal: illegal, N: n}}
	if doReplay {
		evs = append(evs, d.step(replay)...)
	}
	return evs
}

func (d *Decoder) step(b byte) []Event {
	switch d.mode {
	case 1: // 等第二字节
		d.got = 1
		if d.lo2 != 0 || d.hi2 != 0 {
			if b >= d.lo2 && b <= d.hi2 {
				d.mode, d.lo2, d.hi2 = 2, 0, 0
				return nil
			}
			if cont(b) && d.need == 4 { // F0/F4：进入待决残尾
				d.mode, d.rem = 3, d.need-2
				return nil
			}
			return d.settle(scalar.ReplacementRune, true, 1, 1, b, true)
		}
		if cont(b) {
			if d.need == 2 {
				r := rune(d.lead&0x1F)<<6 | rune(b&0x3F)
				return d.settle(r, false, 2, 2, 0, false)
			}
			d.mode = 2
			return nil
		}
		return d.settle(scalar.ReplacementRune, true, 1, 1, b, true)
	case 2: // 等普通续字节
		if cont(b) {
			d.got++
			if d.got == d.need-1 {
				r := rune(d.lead&ldMask(d.need)) << uint(6*(d.need-1))
				for i := 1; i < d.need; i++ {
					r |= rune(d.pend[i]&0x3F) << uint(6*(d.need-1-i))
				}
				return d.settle(r, false, d.need, d.need, 0, false)
			}
			return nil
		}
		return d.settle(scalar.ReplacementRune, true, d.got+1, d.got+1, b, true)
	case 3: // F0/F4 待决残尾
		d.rem--
		if cont(b) && d.rem > 0 {
			return nil
		}
		if cont(b) { // rem==0：续字节恰好填满声明窗口
			evs := d.settle(scalar.ReplacementRune, true, 1, 1, 0, false)
			for _, c := range d.pend {
				evs = append(evs, d.step(c)...)
			}
			return evs
		}
		// rem>0：非续字节提前截断；首字节 + 已吞 (need-1-rem) 个续字节整体一个单元
		n := d.need - 1 - d.rem
		return d.settle(scalar.ReplacementRune, true, n, n, b, true)
	default:
		return d.idle(b)
	}
}

func ldMask(need int) byte {
	return []byte{0, 0, 0x1F, 0x0F, 0x07}[need]
}

func (d *Decoder) idle(b byte) []Event {
	if b < 0x80 {
		d.pend = d.pend[:len(d.pend)-1]
		return []Event{{R: rune(b), N: 1}}
	}
	if cont(b) { // 孤立续字节
		d.pend = d.pend[:len(d.pend)-1]
		return []Event{{R: scalar.ReplacementRune, Illegal: true, N: 1}}
	}
	switch {
	case b >= 0xC2 && b <= 0xDF:
		d.lead, d.need, d.got, d.mode = b, 2, 0, 1
	case b == 0xE0:
		d.lead, d.need, d.mode, d.lo2, d.hi2 = b, 3, 1, 0xA0, 0xbf
	case b >= 0xE1 && b <= 0xEC, b >= 0xEE && b <= 0xEF:
		d.lead, d.need, d.mode = b, 3, 1
	case b == 0xED:
		d.lead, d.need, d.mode, d.lo2, d.hi2 = b, 3, 1, 0x80, 0x9f
	case b == 0xF0:
		d.lead, d.need, d.mode, d.lo2, d.hi2 = b, 4, 1, 0x90, 0xbf
	case b >= 0xF1 && b <= 0xF3:
		d.lead, d.need, d.mode = b, 4, 1
	case b == 0xF4:
		d.lead, d.need, d.mode, d.lo2, d.hi2 = b, 4, 1, 0x80, 0x8f
	default: // C0 C1 F5..FF
		d.pend = d.pend[:len(d.pend)-1]
		return []Event{{R: scalar.ReplacementRune, Illegal: true, N: 1}}
	}
	return nil
}

// Flush 在流结束时结算残尾。确定非法返回事件；仅合法前缀被截断时 truncated。
func (d *Decoder) Flush() (ev Event, truncated bool) {
	if len(d.pend) == 0 {
		return Event{}, false
	}
	n, m := len(d.pend), d.mode
	d.pend, d.mode = nil, 0
	if m == 3 || d.got > 0 {
		return Event{R: scalar.ReplacementRune, Illegal: true, N: n}, false
	}
	return Event{R: scalar.ReplacementRune, Illegal: true, N: n}, true
}

// Pending 返回尚未结算的残尾副本（至多 3 字节）。
func (d *Decoder) Pending() []byte {
	return append([]byte(nil), d.pend...)
}

// Encode 把标量编码成 UTF-8；非标量返回 nil。
func Encode(r rune) []byte {
	if !scalar.IsScalar(r) {
		return nil
	}
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
