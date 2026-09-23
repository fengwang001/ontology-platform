// Package u8 在字节级实现 UTF-8 的逐标量解码与编码。
// 不使用 unicode/utf8、unicode/utf16，也不使用任何隐式 rune 解码。
package u8

import "ontology/scalar"

// Kind 是一个解码单元的类别。
type Kind uint8

const (
	OK       Kind = iota // 合法标量
	Invalid              // 非法单元
	Trunc                // 流结束时残留的未完成合法前缀
)

// Unit 是一次解码结论：合法标量或一个非法/截断单元。
type Unit struct {
	R    scalar.Rune
	Kind Kind
	Size int // 本单元吞掉的字节数
	Start int64 // 单元在整个输入流中的起始字节偏移
}

// Decoder 是可跨 Write 复用的字节级 UTF-8 解码器；零值即可用。
type Decoder struct {
	cp     scalar.Rune
	need   int
	min    scalar.Rune
	lo2    byte
	hi2    byte
	seen   int // 已缓存的合法前缀字节数（含首字节）
	checks int64
	pos    int64 // 已永久消费（含待闭合前缀）的字节数
	start  int64 // 当前前缀首字节偏移
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

// Step 喂入一个字节，返回是否产出单元。
// 非法时 u.Size 为吞掉字节数；触发字节（非续行）不被吞，调用方须重喂。
// Feed 处理一段字节，返回产出的单元与永久消费字节数。
// 触发字节（使前缀非法的非续行/越窗字节）不被消费，会在内部立即重喂。
func (d *Decoder) Feed(p []byte) (units []Unit, consumed int) {
	i := 0
	n := 0
	for i < len(p) {
		d.checks++
		b := p[i]
		if d.need == 0 {
			d.start = d.pos
			d.pos++
			i++
			n++
			switch {
			case b < 0x80:
				units = append(units, Unit{R: scalar.Rune(b), Kind: OK, Size: 1, Start: d.start})
			case b >= 0xC2 && b <= 0xDF:
				d.need, d.min, d.lo2, d.hi2, d.cp, d.seen = 2, 0x80, 0x80, 0xBF, scalar.Rune(b&0x1F), 1
			case b == 0xE0:
				d.need, d.min, d.lo2, d.hi2, d.cp, d.seen = 3, 0xA00, 0xA0, 0xBF, 0, 1
			case b >= 0xE1 && b <= 0xEF:
				d.need, d.min, d.lo2, d.hi2, d.cp, d.seen = 3, 0x800, 0x80, 0xBF, scalar.Rune(b&0x0F), 1
			case b == 0xF0:
				d.need, d.min, d.lo2, d.hi2, d.cp, d.seen = 4, 0x10000, 0x90, 0xBF, 0, 1
			case b >= 0xF1 && b <= 0xF3:
				d.need, d.min, d.lo2, d.hi2, d.cp, d.seen = 4, 0x10000, 0x80, 0xBF, scalar.Rune(b&0x07), 1
			case b == 0xF4:
				d.need, d.min, d.lo2, d.hi2, d.cp, d.seen = 4, 0x10000, 0x80, 0x8F, 4, 1
			default:
				units = append(units, Unit{Kind: Invalid, Size: 1, Start: d.start})
			}
			continue
		}
		if !cont(b) || (d.seen == 1 && (b < d.lo2 || b > d.hi2)) {
			size := d.seen
			lead := d.start
			d.need, d.seen = 0, 0
			units = append(units, Unit{Kind: Invalid, Size: size, Start: lead})
			continue // 触发字节不前进，立即重喂
		}
		d.pos++
		i++
		n++
		d.cp = d.cp<<6 | scalar.Rune(b&0x3F)
		d.seen++
		if d.seen == d.need {
			complete := d.seen
			lead := d.start
			d.need, d.seen = 0, 0
			if d.cp < d.min || !scalar.IsScalar(d.cp) {
				units = append(units, Unit{Kind: Invalid, Size: complete, Start: lead})
			} else {
				units = append(units, Unit{R: d.cp, Kind: OK, Size: complete, Start: lead})
			}
		}
	}
	return units, n
}

// Pending 报告是否缓存了未完成前缀及缓存字节数（上限 3）。
func (d *Decoder) Pending() (n int, ok bool) { return d.seen, d.need > 0 }

// Flush 在流结束时调用：残留合法前缀产出一个截断单元。
func (d *Decoder) Flush() (Unit, bool) {
	if d.need == 0 {
		return Unit{}, false
	}
	n := d.seen
	lead := d.start
	d.need, d.seen = 0, 0
	return Unit{Kind: Trunc, Size: n, Start: lead}, true
}

// Pos 返回已永久消费（含待闭合前缀）的字节数。
func (d *Decoder) Pos() int64 { return d.pos }

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Len 返回标量编码为 UTF-8 的字节数。
func Len(r scalar.Rune) int {
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

// Encode 把一个标量追加编码到 dst，非法码点替换为 U+FFFD。
func Encode(dst []byte, r scalar.Rune) []byte {
	if !scalar.IsScalar(r) {
		r = scalar.Replacement
	}
	switch n := Len(r); {
	case n == 1:
		return append(dst, byte(r))
	case n == 2:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case n == 3:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
