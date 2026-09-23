// Package u16 用纯字节判定实现 UTF-16LE / UTF-16BE 的增量解码与编码，
// 含代理对合并与孤立代理处理。禁止使用 unicode/utf16。
package u16

import "ontology/scalar"

// 字节序。
const (
	LE = iota
	BE
)

// 事件类型。
const (
	EvNeed  = iota // 还需字节
	EvOK           // 合法标量
	EvBad          // 一个非法单元（孤立代理）
	EvBOM          // 流开头读到 U+FEFF（调用方决定丢弃或输出）
)

// Result 是喂入字节后的结果。
type Result struct {
	Event   int
	Rune    rune
	UnitLen int    // EvBad 时非法单元吞掉的字节数（孤立代理=2）
	RP      []byte // 不属于本单元、需重新喂入的字节
	B0, B1  byte   // 组成当前码元的两个原始字节
}

// Decoder 是单实例、非并发安全的增量 UTF-16 解码器。
type Decoder struct {
	order int
	first bool
	hi    bool   // 上一个码元是高代理，等待低代理
	hiv   uint16 // 待配对的高代理
	lo    byte   // 已缓存的半个码元
	have  bool
}

// NewDecoder 按字节序创建解码器。
func NewDecoder(order int) *Decoder { return &Decoder{order: order, first: true} }

func (d *Decoder) unit(lo, hi byte) uint16 {
	if d.order == LE {
		return uint16(lo) | uint16(hi)<<8
	}
	return uint16(hi) | uint16(lo)<<8
}

// Feed 喂入一个字节。
func (d *Decoder) Feed(b byte) Result {
	if !d.have {
		d.lo, d.have = b, true
		return Result{Event: EvNeed}
	}
	d.have = false
	u := d.unit(d.lo, b)
	rb0, rb1 := d.lo, b
	switch {
	case d.hi && scalar.LowSurrogate(rune(u)):
		d.hi = false
		r := scalar.FromSurrogatePair(d.hiv, u)
		d.first = false
		return Result{Event: EvOK, Rune: r, UnitLen: 4, B0: rb0, B1: rb1}
	case d.hi:
		// 高代理后跟非低代理：高代理是一个非法单元；当前码元必须被重新
		// 当作新字符处理，故连同其两个字节整体归还。
		d.hi = false
		return Result{Event: EvBad, UnitLen: 2, RP: []byte{rb0, rb1}, B0: rb0, B1: rb1}
	case scalar.HighSurrogate(rune(u)):
		d.hi, d.hiv = true, u
		return Result{Event: EvNeed}
	case scalar.Surrogate(rune(u)):
		return Result{Event: EvBad, UnitLen: 2, B0: rb0, B1: rb1}
	default:
		if d.first && u == 0xFEFF {
			d.first = false
			return Result{Event: EvBOM, Rune: rune(u), UnitLen: 2, B0: rb0, B1: rb1}
		}
		d.first = false
		return Result{Event: EvOK, Rune: rune(u), UnitLen: 2, B0: rb0, B1: rb1}
	}
}

// SetOrder 在流开头 BOM 确定字节序后切换（供 stream 使用）。
func (d *Decoder) SetOrder(order int) { d.order = order }

// PendingLen 返回缓存字节数：半个码元=1，等待低代理=2（另含待配对语义）。
func (d *Decoder) PendingLen() int {
	n := 0
	if d.have {
		n++
	}
	if d.hi {
		n += 2
	}
	return n
}

// Close 报告流结束是否截断（奇数字节或残留高代理）。
func (d *Decoder) Close() bool { return d.have || d.hi }

// Encode 把合法标量按序写入 buf（4 字节工作区），返回写入字节数。
func Encode(buf []byte, r rune, order int) int {
	if r >= 0x10000 {
		hi, lo := scalar.ToSurrogatePair(r)
		put(buf[0:2], hi, order)
		put(buf[2:4], lo, order)
		return 4
	}
	put(buf[0:2], uint16(r), order)
	return 2
}

// EncodeLen 返回标量的 UTF-16 编码字节数（2 或 4）。
func EncodeLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

func put(b []byte, u uint16, order int) {
	if order == LE {
		b[0], b[1] = byte(u), byte(u>>8)
	} else {
		b[0], b[1] = byte(u>>8), byte(u)
	}
}
