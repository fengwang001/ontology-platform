// Package u8 手写 UTF-8 逐标量解码（不使用 unicode/utf8）。
package u8

import "ontology/scalar"

// Kind 区分事件类型。
type Kind int

const (
	KindScalar Kind = iota
	KindInvalid
	KindTrunc
)

// Event 是一次解码结果。KindScalar 时 Scalar 有效；否则 N 为吞掉字节数。
type Event struct {
	Scalar rune
	N      int
	Kind   Kind
	Off    int64 // 单元在已喂入总字节中的起始偏移
}

// Decoder 是可跨切分点的增量解码器。
type Decoder struct {
	pend [3]byte // 已通过检查但未成序列的字节（硬上限 3）
	np   int
	lead scalar.Lead
	// Checks 记录字节被检查的总次数（每字节至多 2 次）。
	Checks    int64
	fed       int64
	pendStart int64
}

// NewDecoder 创建解码器。
func NewDecoder() *Decoder { return &Decoder{} }

// Feed 喂入字节。cb 返回 false 立即停止；返回推进到的字节下标
// （未结算的合法前缀不计，供 Write 计算可消费字节数）。
func (d *Decoder) Feed(p []byte, cb func(Event) bool) int {
	i := 0
	recheck := false // 当前字节是被退回重解析的，不再重复计 Checks
	for i < len(p) {
		b := p[i]
		if !recheck {
			d.Checks++
		}
		recheck = false
		off := d.fed + int64(i)
		if d.np == 0 {
			if b < 0x80 {
				i++
				if !cb(Event{Scalar: rune(b), N: 1, Kind: KindScalar, Off: off}) {
					return i - 1
				}
				continue
			}
			ld := scalar.DecodeLead(b)
			if ld.Len == 0 {
				i++
				if !cb(Event{N: 1, Kind: KindInvalid, Off: off}) {
					return i - 1
				}
				continue
			}
			d.lead, d.pendStart, d.pend[0], d.np = ld, off, b, 1
			i++
			continue
		}
		bad := (d.np == 1 && (b < d.lead.SecondLo || b > d.lead.SecondHi)) ||
			(d.np > 1 && !scalar.IsCont(b))
		if bad {
			so, sn := d.pendStart, d.np
			d.np, d.lead = 0, scalar.Lead{}
			if !cb(Event{N: sn, Kind: KindInvalid, Off: so}) {
				return i
			}
			recheck = true // b 未入会，回退作为新起点
			continue
		}
		d.pend[d.np] = b
		d.np++
		i++
		if d.np == d.lead.Len {
			r := assemble(d.pend[:d.np])
			so, sn := d.pendStart, d.np
			d.np, d.lead = 0, scalar.Lead{}
			if !cb(Event{Scalar: r, N: sn, Kind: KindScalar, Off: so}) {
				return i
			}
		}
	}
	return i
}

func assemble(p []byte) rune {
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

// Pending 返回尚未成序列的合法前缀字节数（0..3）。
func (d *Decoder) Pending() int { return d.np }

// PendingStart 返回未完成前缀的全局起始偏移。
func (d *Decoder) PendingStart() int64 { return d.pendStart }

// PendingBytes 拷贝返回当前缓存字节。
func (d *Decoder) PendingBytes() []byte {
	out := make([]byte, d.np)
	copy(out, d.pend[:d.np])
	return out
}

// MarkFed 告知 n 个字节已归属（跨 Write 的偏移记账）。
func (d *Decoder) MarkFed(n int) { d.fed += int64(n) }

// End 标记流结束：残留合法前缀报告一个截断单元。
func (d *Decoder) End(cb func(Event) bool) {
	if d.np > 0 {
		so, sn := d.pendStart, d.np
		d.np = 0
		cb(Event{N: sn, Kind: KindTrunc, Off: so})
	}
}

// Encode 把一个标量编码为 UTF-8，追加到 dst。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F,
			0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
