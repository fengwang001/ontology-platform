// Package u16 在字节级实现 UTF-16LE/UTF-16BE 的解码与编码。
// 不使用 unicode/utf16；代理对逻辑手写。
package u16

import "ontology/scalar"

// Unit 是一次 UTF-16 解码事件。
type Unit struct {
	R   rune
	Len int   // 吞掉的 code unit 数：合法标量 1 或 2，非法单元 1
	OK  bool
	Off int64 // 起始字节偏移（基于整个输入流）
}

// Decoder 跨 Write 解码 UTF-16；单实例非并发安全。
type Decoder struct {
	be     bool
	odd    byte // 残留的半个 code unit
	hasOdd bool
	hi     uint16 // 等待低代理的高代理
	hasHi  bool
	base   int64  // 已被事件消费的字节数
	check  int64  // 字节被检查总次数
}

// NewDecoder 构造解码器；be 选择字节序。
func NewDecoder(be bool) *Decoder { return &Decoder{be: be} }

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.check }

// Pending 返回缓存字节数：半个 code unit 时为 1（硬上限 2 含待配代理）。
func (d *Decoder) Pending() int {
	if d.hasOdd {
		return 1
	}
	return 0
}

// Feed 喂入原始字节，产出解码事件。
func (d *Decoder) Feed(p []byte) (units []Unit) {
	i := 0
	if d.hasOdd && len(p) > 0 {
		d.check++
		cu := d.assemble(d.odd, p[0])
		d.hasOdd = false
		i = 1
		units = append(units, d.feedCU(cu)...)
	}
	for ; i+1 < len(p); i += 2 {
		d.check += 2
		units = append(units, d.feedCU(d.assemble(p[i], p[i+1]))...)
	}
	if i < len(p) {
		d.odd, d.hasOdd = p[i], true
	}
	return units
}

func (d *Decoder) feedCU(cu uint16) []Unit {
	r := rune(cu)
	switch {
	case scalar.HighSurrogate(r):
		if d.hasHi { // 上一个孤立高代理先成非法单元
			u := Unit{Len: 1, Off: d.base}
			d.base += 2
			d.hi = cu // 新高代理继续等待低代理
			return []Unit{u}
		}
		d.hasHi, d.hi = true, cu
		return nil
	case scalar.LowSurrogate(r):
		if d.hasHi {
			rr := scalar.FromSurrogatePair(d.hi, cu)
			d.hasHi = false
			u := Unit{R: rr, Len: 2, OK: true, Off: d.base}
			d.base += 4
			return []Unit{u}
		}
		u := Unit{Len: 1, Off: d.base} // 孤立低代理
		d.base += 2
		return []Unit{u}
	default:
		if d.hasHi { // 高代理后跟非低代理：高代理非法，cu 重新解析
			d.hasHi = false
			u := Unit{Len: 1, Off: d.base}
			d.base += 2
			return []Unit{u, d.feedBMP(cu)}
		}
		return []Unit{d.feedBMP(cu)}
	}
}

func (d *Decoder) feedBMP(cu uint16) Unit {
	u := Unit{R: rune(cu), Len: 1, OK: !scalar.Surrogate(rune(cu)), Off: d.base}
	d.base += 2
	return u
}

func (d *Decoder) assemble(lo, hiByte byte) uint16 {
	if d.be {
		return uint16(lo)<<8 | uint16(hiByte)
	}
	return uint16(hiByte)<<8 | uint16(lo)
}

// End 报告流结束残留：奇数字节或残留高代理均为截断，二者合并由 stream 区分。
func (d *Decoder) End() (Unit, bool) {
	switch {
	case d.hasOdd:
		d.hasOdd = false
		return Unit{Len: 1, Off: d.base}, true
	case d.hasHi:
		d.hasHi = false
		return Unit{Len: 1, Off: d.base}, true
	default:
		return Unit{}, false
	}
}

// PendingHi 报告是否残留一个等待低代理的高代理。
func (d *Decoder) PendingHi() bool { return d.hasHi }

// Encode 把标量编码为指定字节序的 UTF-16；BMP 外编成代理对。
func Encode(r rune, be bool) []byte {
	var out []byte
	put := func(cu uint16) {
		if be {
			out = append(out, byte(cu>>8), byte(cu))
		} else {
			out = append(out, byte(cu), byte(cu>>8))
		}
	}
	switch {
	case !scalar.Valid(r):
		return nil
	case r >= 0x10000:
		hi, lo := scalar.SurrogatePair(r)
		put(hi)
		put(lo)
	default:
		put(uint16(r))
	}
	return out
}
