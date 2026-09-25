// Package u8 在字节级解码/编码 UTF-8，不用 unicode/utf8。依赖 scalar。
package u8

import "ontology/scalar"

// UnitKind 一个解码单元的类别。
type UnitKind int

const (
	KindASCII      UnitKind = iota // 单字节 ASCII
	KindScalar                     // 合法多字节标量
	KindBad                        // 非法单元
	KindIncomplete                 // 合法前缀但输入被截断
)

// Unit 一次解码结果：类别、标量（坏/截断时为 U+FFFD）、起始偏移、吞掉字节数。
type Unit struct {
	Kind UnitKind
	R    rune
	Off  int
	N    int
}

// Decoder 增量 UTF-8 解码器。held（首字节 + 续写）至多 4 字节。
type Decoder struct {
	buf      [4]byte
	start    int // 首字节在全流中的偏移
	held     int
	need     int // 该序列总长度
	checks   int64
	off      int // 已被完整单元消费的字节数
	emittedB int // 当前流中已被完整单元消费的字节数（用于 n 计算）
}

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Held 返回当前持有的未决字节数（含跨 Write 的半个字符）。
func (d *Decoder) Held() int { return d.held }

// HeldBytes 返回当前持有字节的副本（用于上限续传）。
func (d *Decoder) HeldBytes() []byte { return d.buf[:d.held] }

// Step 从 p[i:] 推进一步；eof 表示流结束。
// 返回单元、输入下标 i 的新值；无单元可输出时 u.N==0。
func (d *Decoder) Step(p []byte, i int, eof bool) (Unit, int) {
	if d.held == 0 {
		if i >= len(p) {
			return Unit{}, i
		}
		b := p[i]
		d.checks++
		if b < scalar.RuneSelf {
			d.off++
			return Unit{Kind: KindASCII, R: rune(b), Off: d.off - 1, N: 1}, i + 1
		}
		d.need = scalar.SeqLen(b)
		d.start = d.off
		if d.need == 0 {
			d.off++
			return Unit{Kind: KindBad, R: scalar.Replacement, Off: d.start, N: 1}, i + 1
		}
		d.buf[0] = b
		d.held = 1
		i++
	}
	// 已持有合法前缀，收集续写。
	need := d.need
	got := 0
	ok := true
	for d.held < need {
		if i >= len(p) {
			if eof {
				if ok {
					u := Unit{Kind: KindIncomplete, R: scalar.Replacement, Off: d.start, N: d.held}
					d.truncate(need)
					return u, i
				}
				u := Unit{Kind: KindBad, R: scalar.Replacement, Off: d.start, N: d.held}
				d.truncate(need)
				return u, i
			}
			return Unit{}, i
		}
		b := p[i]
		d.checks++
		if d.held == 1 {
			if !scalar.SecondOK(d.buf[0], b) {
				ok = false
				break
			}
		} else if !scalar.IsContinuation(b) {
			ok = false
			break
		}
		d.buf[d.held] = b
		d.held++
		got++
		i++
	}
	if ok {
		r := decode(d.buf[:need])
		u := Unit{Kind: KindScalar, R: r, Off: d.start, N: need}
		d.off += need
		d.held = 0
		return u, i
	}
	// 首字节非法：吞 1，回退已吸收的续写字节（它们会被主循环重查，计数 ≤2N）。
	_ = got
	u := Unit{Kind: KindBad, R: scalar.Replacement, Off: d.start, N: 1}
	d.off++
	i -= d.held - 1
	d.held = 0
	return u, i
}

// truncate 结算一个在流结束时仍未闭合的单元。
func (d *Decoder) truncate(need int) {
	d.off += d.held
	d.held = 0
	_ = need
}

// decode 由已验证长度的字节组装标量。
func decode(b []byte) rune {
	switch len(b) {
	case 2:
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F)
	case 3:
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
	default:
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
	}
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

// Encode 把标量追加编码到 dst。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
