// Package u8 提供字节级 UTF-8 逐标量解码与编码（不使用 unicode/utf8）。
package u8

import "ontology/scalar"

const replacement = '\uFFFD'

// Event 是 DFA 每推进一步吐出的单元。
type Event struct {
	Rune     rune
	Bad      bool // 非法单元
	Consumed int  // 本单元吞掉的字节数
	Fed      bool // 喂入字节是否被本单元消费；false 表示需原样重放
}

// Decoder 是跨 Write 的字节级 DFA。零值不可直接使用，用 NewDecoder。
type Decoder struct {
	first byte
	need  int
	got   int
	val   rune
	pend  []byte
	seen  int64
}

// NewDecoder 创建解码器。checks 累加「字节被检查的总次数」，可为 nil。
func NewDecoder(checks *int64) *Decoder { return &Decoder{} }

func isCont(b byte) bool { return b&0xC0 == 0x80 }

// leadLen 返回合法首字节的字符长度；非法首字节（含连续字节）返回 0。
func leadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	case b >= 0xF0 && b <= 0xF4:
		return 4
	}
	return 0
}

// secondOK 报告首字节 first 之后的第二字节是否在合法区间。
func secondOK(first, b byte) bool {
	switch first {
	case 0xE0:
		return b >= 0xA0 && b <= 0xBF
	case 0xED:
		return b >= 0x80 && b <= 0x9F
	case 0xF0:
		return b >= 0x90 && b <= 0xBF
	case 0xF4:
		return b >= 0x80 && b <= 0x8F
	}
	return isCont(b)
}

// Pending 返回缓存的未完成合法前缀（长度 0..3）。
func (d *Decoder) Pending() []byte { return d.pend }

// Seen 返回已进入解码器的字节总数（含 Pending）。
func (d *Decoder) Seen() int64 { return d.seen }

// Reset 清空缓存（已提交字节计数保留）。
func (d *Decoder) Reset() { d.pend = d.pend[:0]; d.need, d.got = 0, 0 }

// Step 喂入一个字节。非法首字节/孤立连续字节立即成单元；第二字节越界或
// 后续字节非连续时吐出已缓存前缀单元且 Fed=false（该字节未被吞，须重放）；
// 合法前缀未完成时 ok=false。
func (d *Decoder) Step(b byte) (ev Event, ok bool) {
	d.seen++
	if d.need == 0 {
		if b < 0x80 {
			return Event{Rune: rune(b), Consumed: 1, Fed: true}, true
		}
		if n := leadLen(b); n > 0 {
			d.first, d.need, d.got = b, n, 1
			d.pend = append(d.pend[:0], b)
			return Event{}, false
		}
		return Event{Rune: replacement, Bad: true, Consumed: 1, Fed: true}, true
	}
	if d.got == 1 && !secondOK(d.first, b) {
		n := len(d.pend)
		d.Reset()
		return Event{Rune: replacement, Bad: true, Consumed: n, Fed: false}, true
	}
	d.pend = append(d.pend, b)
	if !isCont(b) {
		n := len(d.pend) - 1
		d.Reset()
		return Event{Rune: replacement, Bad: true, Consumed: n, Fed: false}, true
	}
	d.got++
	if d.got < d.need {
		return Event{}, false
	}
	// 长度在此固定 2..4，直接按位组装标量。
	p := d.pend
	var r rune
	switch len(p) {
	case 2:
		r = rune(p[0]&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		r = rune(p[0]&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	default:
		r = rune(p[0]&0x07)<<18 | rune(p[1]&0x3F)<<12 | rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
	n := len(p)
	d.Reset()
	ev = Event{Rune: r, Consumed: n, Fed: true}
	if !scalar.Valid(r) {
		ev.Rune, ev.Bad = replacement, true
	}
	return ev, true
}

// Flush 在流结束时调用：残留合法前缀作为一个非法单元吐出（吞掉全部缓存字节）。
func (d *Decoder) Flush() (ev Event, ok bool) {
	if d.need == 0 {
		return Event{}, false
	}
	n := len(d.pend)
	d.Reset()
	return Event{Rune: replacement, Bad: true, Consumed: n, Fed: true}, true
}

// AppendEncode 把合法标量编码为 UTF-8 追加到 dst。
func AppendEncode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|(byte(r)&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|(byte(r>>6)&0x3F), 0x80|(byte(r)&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|(byte(r>>12)&0x3F), 0x80|(byte(r>>6)&0x3F), 0x80|(byte(r)&0x3F))
	}
}

// AlignStart 返回使解析安全开始的字节偏移：从 cut 向前最多 3 字节找最近的
// 非连续字节 j；若 j 是合法多字节首字节且其单元可能跨过 cut，返回 j，否则返回 cut。
func AlignStart(buf []byte, cut int) int {
	lo := cut - 3
	if lo < 0 {
		lo = 0
	}
	for j := cut - 1; j >= lo; j-- {
		b := buf[j]
		if isCont(b) {
			continue
		}
		if n := leadLen(b); n > 1 && j+n > cut {
			return j
		}
		return cut
	}
	return cut
}
