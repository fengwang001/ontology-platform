// Package u8 提供逐字节的 UTF-8 解码与编码，不使用 unicode/utf8。
package u8

// 解码事件类型。
const (
	EvScalar = iota // R 为合法标量
	EvNeed          // 数据不足，需更多字节（仅在末尾）
	EvBad           // 一个非法单元，UnitLen 个字节被吞掉
)

// Event 是一次 Step 的结果。
type Event struct {
	Kind    int
	R       rune
	UnitLen int // EvBad 时非法单元长度；EvScalar 时序列长度
}

// Decoder 是跨 Write 保留合法前缀的 UTF-8 解码器。
type Decoder struct {
	buf    [3]byte // 未完成的合法前缀（不含尚未交付的部分）
	n      int     // buf 中有效字节数（1..3）
	need   int     // 首字节声明的总长度
	r      rune    // 已累计的值
	Checks int     // 字节被检查次数
}

// PendingLen 返回缓存中尚未完成的合法前缀字节数（硬上限 3）。
func (d *Decoder) PendingLen() int { return d.n }

// Reset 清空解码器状态。
func (d *Decoder) Reset() { d.n, d.need, d.r, d.Checks = 0, 0, 0, 0 }

// Drain 取出当前缓存的未完成合法前缀并清空状态，追加到 out。
func (d *Decoder) Drain(out []byte) []byte {
	out = append(out, d.buf[:d.n]...)
	d.n, d.need = 0, 0
	return out
}

// lead 描述首字节：总长度、第二字节区间、已解析出的初值。
func lead(b byte) (need int, lo, hi byte, r rune, ok bool) {
	switch {
	case b < 0x80:
		return 1, 0, 0, rune(b), true
	case b >= 0xC2 && b <= 0xDF:
		return 2, 0x80, 0xBF, rune(b & 0x1F), true
	case b == 0xE0:
		return 3, 0xA0, 0xBF, 0, true
	case b >= 0xE1 && b <= 0xEC:
		return 3, 0x80, 0xBF, rune(b & 0x0F), true
	case b == 0xED:
		return 3, 0x80, 0x9F, 0x0D, true
	case b >= 0xEE && b <= 0xEF:
		return 3, 0x80, 0xBF, rune(b & 0x0F), true
	case b == 0xF0:
		return 4, 0x90, 0xBF, 0, true
	case b >= 0xF1 && b <= 0xF3:
		return 4, 0x80, 0xBF, rune(b & 0x07), true
	case b == 0xF4:
		return 4, 0x80, 0x8F, 0x04, true
	}
	return 0, 0, 0, 0, false
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

// Step 从 p 解析一个单元，返回事件与本次消费的字节数。
// EvBad 时被吞掉的前缀可能来自跨 Write 缓存，consumed 仅统计来自 p 的字节。
func (d *Decoder) Step(p []byte) (ev Event, consumed int) {
	if d.n == 0 {
		if len(p) == 0 {
			return Event{Kind: EvNeed}, 0
		}
		d.Checks++
		b := p[0]
		if b < 0x80 {
			return Event{Kind: EvScalar, R: rune(b), UnitLen: 1}, 1
		}
		need, _, _, r, ok := lead(b)
		if !ok {
			return Event{Kind: EvBad, UnitLen: 1}, 1
		}
		d.need, d.r = need, r
		d.buf[0] = b
		d.n = 1
		p = p[1:]
		consumed = 1
	}
	// 用首字节重新求第二字节区间。
	_, lo, hi, _, _ := lead(d.buf[0])
	for d.n < d.need {
		if len(p) == 0 {
			return Event{Kind: EvNeed}, consumed
		}
		d.Checks++
		b := p[0]
		validRange := d.n != 1 || (b >= lo && b <= hi)
		if !cont(b) || !validRange {
			ul := d.n
			d.n, d.need = 0, 0
			return Event{Kind: EvBad, UnitLen: ul}, consumed
		}
		d.r = d.r<<6 | rune(b&0x3F)
		d.buf[d.n] = b
		d.n++
		p = p[1:]
		consumed++
	}
	r, ul := d.r, d.n
	d.n, d.need = 0, 0
	return Event{Kind: EvScalar, R: r, UnitLen: ul}, consumed
}

// Flush 在流结束时调用：残留合法前缀按截断处理（非法单元）。
func (d *Decoder) Flush() (Event, bool) {
	if d.n == 0 {
		return Event{}, false
	}
	ul := d.n
	d.n, d.need = 0, 0
	return Event{Kind: EvBad, UnitLen: ul}, true
}

// EncodeLen 返回标量的 UTF-8 编码长度。
func EncodeLen(r rune) int {
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

// Encode 把标量写入 b（容量须 ≥ EncodeLen），返回写入字节数。
func Encode(b []byte, r rune) int {
	switch n := EncodeLen(r); n {
	case 1:
		b[0] = byte(r)
	case 2:
		b[0] = byte(0xC0 | r>>6)
		b[1] = byte(0x80 | r&0x3F)
	case 3:
		b[0] = byte(0xE0 | r>>12)
		b[1] = byte(0x80 | (r>>6)&0x3F)
		b[2] = byte(0x80 | r&0x3F)
	default:
		b[0] = byte(0xF0 | r>>18)
		b[1] = byte(0x80 | (r>>12)&0x3F)
		b[2] = byte(0x80 | (r>>6)&0x3F)
		b[3] = byte(0x80 | r&0x3F)
		return n
	}
	return EncodeLen(r)
}

// TailCarry 报告 seg 末尾应交给下一段的字节数（≤3）。
// 仅当 seg 以一个仍可能合法的多字节前缀结尾时才移交。
func TailCarry(seg []byte) int {
	for k := 1; k <= 3 && k <= len(seg); k++ {
		i := len(seg) - k
		need, lo, hi, _, ok := lead(seg[i])
		if !ok || need == 1 {
			continue
		}
		if len(seg)-i >= need {
			continue
		}
		good := true
		for j := i + 1; j < len(seg); j++ {
			b := seg[j]
			if !cont(b) || (j == i+1 && !(b >= lo && b <= hi)) {
				good = false
			}
		}
		if good {
			return k
		}
	}
	return 0
}
