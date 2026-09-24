// Package u8 在字节级实现 UTF-8 的逐标量解码与编码。
// 不使用 unicode/utf8，所有判定手写。
package u8

import "ontology/scalar"

// SeqLen 返回首字节声明的序列长度（1..4）；非法首字节返回 0。
func SeqLen(b0 byte) int {
	switch {
	case b0 < 0x80:
		return 1
	case b0 >= 0xC2 && b0 <= 0xDF:
		return 2
	case b0 >= 0xE0 && b0 <= 0xEF:
		return 3
	case b0 >= 0xF0 && b0 <= 0xF4:
		return 4
	default:
		return 0
	}
}

// Cont 报告 b 是否为续接字节 10xxxxxx。
func Cont(b byte) bool { return b&0xC0 == 0x80 }

// SecondOK 报告第二字节是否落在首字节专属的合法区间内。
func SecondOK(b0, b1 byte) bool {
	switch {
	case b0 == 0xE0:
		return b1 >= 0xA0 && b1 <= 0xBF
	case b0 == 0xED:
		return b1 >= 0x80 && b1 <= 0x9F
	case b0 == 0xF0:
		return b1 >= 0x90 && b1 <= 0xBF
	case b0 == 0xF4:
		return b1 >= 0x80 && b1 <= 0x8F
	default: // C2..DF, E1..EC, EE..EF, F1..F3
		return Cont(b1)
	}
}

// Encode 把合法标量编码为 UTF-8 字节；非法标量返回 nil。
func Encode(r rune) []byte {
	switch {
	case !scalar.Valid(r):
		return nil
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r)&0x3F}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F,
			0x80 | byte(r>>6)&0x3F, 0x80 | byte(r)&0x3F}
	}
}

// Unit 是一次解码事件：合法标量（OK=true）或一个非法单元。
type Unit struct {
	R    rune
	Len  int  // 本单元吞掉的输入字节数
	OK   bool // 是否为合法标量
	Off  int64 // 本单元在整个输入流中的起始字节偏移
}

// Decoder 是跨 Write 的 UTF-8 解码器；单实例非并发安全。
type Decoder struct {
	buf   [3]byte // 已收下、尚未成标的合法前缀
	have  int
	need  int     // 首字节声明的总长度
	b0    byte
	stage int     // 2=第二字节已合法，等待第3..need 个续接字节
	base  int64   // 已被完整事件消费的字节数（事件起始偏移用）
	check int64   // 字节被检查总次数
}

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.check }

// Pending 返回仍在等待的合法前缀字节数（硬上限 3）。
func (d *Decoder) Pending() int { return d.have }

// Seed 用一段结构合法的前缀热启动（供 par 对齐切点）。
func (d *Decoder) Seed(p []byte) {
	d.b0 = p[0]
	d.need = SeqLen(d.b0)
	copy(d.buf[:], p)
	d.have = len(p)
	d.stage = d.have
}

// SeedBytes 返回当前种子（含首字节的原始字节）。
func (d *Decoder) SeedBytes() []byte { return d.buf[:d.have] }

// Feed 喂入字节，产出 0 或多个解码事件。
func (d *Decoder) Feed(p []byte) (units []Unit) {
	i := 0
	for i < len(p) {
		d.check++
		if d.have == 0 {
			b0 := p[i]
			l := SeqLen(b0)
			i++
			if l == 1 {
				units = append(units, Unit{R: rune(b0), Len: 1, OK: true, Off: d.base})
				d.base++
				continue
			}
			if l == 0 {
				units = append(units, Unit{Len: 1, Off: d.base})
				d.base++
				continue
			}
			d.b0, d.need, d.have, d.stage = b0, l, 1, 1
			d.buf[0] = b0
			continue
		}
		b := p[i]
		if d.have == 1 {
			if !SecondOK(d.b0, b) { // b0 单独成非法单元，b 重解析
				units = append(units, Unit{Len: 1, Off: d.base})
				d.base++
				d.have = 0
				continue
			}
		} else if !Cont(b) { // 已收前缀整体成一个非法单元，b 重解析
			units = append(units, Unit{Len: d.have, Off: d.base})
			d.base += int64(d.have)
			d.have = 0
			continue
		}
		d.buf[d.have] = b
		d.have++
		i++
		if d.have == d.need {
			r := decode(d.buf[:d.need])
			units = append(units, Unit{R: r, Len: d.need, OK: true, Off: d.base})
			d.base += int64(d.need)
			d.have = 0
		}
	}
	return units
}

// End 处理流结束：残留前缀整体算一个截断单元（非法单元，长度即前缀长）。
func (d *Decoder) End() (Unit, bool) {
	if d.have == 0 {
		return Unit{}, false
	}
	u := Unit{Len: d.have, Off: d.base}
	d.base += int64(d.have)
	d.have = 0
	return u, true
}

func decode(p []byte) rune {
	switch len(p) {
	case 2:
		return rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		return rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	default:
		return rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 |
			rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
}
