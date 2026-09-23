// Package u8 在字节级别实现增量 UTF-8 解码与编码，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// 事件类别：合法标量 / 非法单元。
const (
	KindOK   = 0
	KindBad  = 1
	KindTrunc = 2
)

// Event 描述一次解码结果。Rune 仅在 KindOK 时有意义；Len 为本单元吞掉的字节数。
type Event struct {
	Kind int
	Rune scalar.Rune
	Len  int
}

// Decoder 是可跨 Write 的增量 UTF-8 状态机。
type Decoder struct {
	state  int // 0=接受首字节，>0=已累积的字节数
	need   int // 期望总长度
	cp     scalar.Rune
	start  int // 本单元在喂入序列中的起始下标（相对永久消费）
	checks int64
}

// NewDecoder 返回初始化解码器。
func NewDecoder() *Decoder { return &Decoder{} }

// Checks 返回字节被检查的次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Pending 返回尚未解决、需要缓冲的字节数（合法前缀）。
func (d *Decoder) Pending() int { return d.state }

func cont(b byte) bool { return b&0xC0 == 0x80 }

// Feed 喂入一个字节。refeed 表示该字节是上次非法事件后重新处理的字节。
// 返回的 Event 若 Len==0 表示尚无完整单元；refeedNext 为 true 时调用方必须用
// refeed=true 再喂一次同一个字节。
func (d *Decoder) Feed(b byte, at int, refeed bool) (ev Event, refeedNext bool) {
	d.checks++
	if d.state == 0 {
		switch {
		case b < 0x80:
			return Event{KindOK, scalar.Rune(b), 1}, false
		case b >= 0xC2 && b <= 0xDF:
			d.state, d.need, d.start = 1, 2, at
			d.cp = scalar.Rune(b & 0x1F)
		case b == 0xE0 || b == 0xED:
			d.state, d.need, d.start = 1, 3, at
			d.cp = scalar.Rune(b&0x0F) << 12
		case (b >= 0xE1 && b <= 0xEC) || (b >= 0xEE && b <= 0xEF):
			d.state, d.need, d.start = 1, 3, at
			d.cp = scalar.Rune(b&0x0F) << 12
		case b == 0xF0 || b == 0xF4:
			d.state, d.need, d.start = 1, 4, at
			d.cp = scalar.Rune(b&0x07) << 18
		case b >= 0xF1 && b <= 0xF3:
			d.state, d.need, d.start = 1, 4, at
			d.cp = scalar.Rune(b&0x07) << 18
		default: // 80..BF、C0、C1、F5..FF
			return Event{Kind: KindBad, Len: 1}, false
		}
		return Event{}, false
	}
	// 第二字节有特殊区间；第三/四字节仅要求续字节。
	ok := cont(b)
	if d.state == 1 {
		switch d.need {
		case 2:
			ok = b >= 0x80 && b <= 0xBF
		case 3:
			switch {
			case d.cp == 0: // E0
				ok = b >= 0xA0 && b <= 0xBF
			case d.cp == 0xD000: // ED
				ok = b >= 0x80 && b <= 0x9F
			}
		case 4:
			switch {
			case d.cp == 0: // F0
				ok = b >= 0x90 && b <= 0xBF
			case d.cp == 0x100000: // F4
				ok = b >= 0x80 && b <= 0x8F
			}
		}
	}
	if !ok {
		len := d.state
		d.state = 0
		return Event{Kind: KindBad, Len: len}, true // 当前字节重新处理
	}
	shift := uint(6*(d.need-d.state-1))
	d.cp |= scalar.Rune(b&0x3F) << shift
	d.state++
	if d.state == d.need {
		r := d.cp
		d.state = 0
		return Event{Kind: KindOK, Rune: r, Len: d.need}, false
	}
	return Event{}, false
}

// Flush 在流结束时调用：残留合法前缀返回一个截断/非法单元。
func (d *Decoder) Flush() Event {
	if d.state == 0 {
		return Event{}
	}
	len := d.state
	d.state = 0
	return Event{Kind: KindTrunc, Len: len}
}

// Encode 把标量编码为 UTF-8 追加到 dst，非法码点原样返回 dst。
func Encode(dst []byte, r scalar.Rune) []byte {
	switch scalar.RuneLen(r) {
	case 1:
		return append(dst, byte(r))
	case 2:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case 3:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	case 4:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F),
			0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
	}
	return dst
}
