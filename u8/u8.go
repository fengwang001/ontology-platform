// Package u8 在字节级别实现 UTF-8 的逐标量解码与编码。
// 不使用 unicode/utf8 或任何隐式字符串解码。
package u8

// Kind 是一次解码事件的类型。
type Kind uint8

const (
	Scalar Kind = iota // 一个合法标量值
	Bad                // 一个非法单元
)

// Event 是一次解码事件。Len 为该单元吞掉的字节数；Scalar 时 R 有效。
type Event struct {
	R    rune
	Kind Kind
	Len  int
}

func cont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// Decoder 是增量 UTF-8 字节状态机；零值可用。
// 调用方把字节逐个喂入：返回 nil 表示该字节并入未完成前缀；
// 返回事件表示闭合；违规字节由调用方重新喂入本 Decoder。
type Decoder struct {
	lead byte
	r    rune
	need int // 还需几个续字节
	seen int // 已并入前缀的字节数（含首字节）
}

// Reset 清空状态（流边界处使用）。
func (d *Decoder) Reset() { *d = Decoder{} }

// InProgress 报告是否存在未闭合的合法前缀。
func (d *Decoder) InProgress() bool { return d.seen > 0 }

// Seen 返回已并入未完成前缀的字节数。
func (d *Decoder) Seen() int { return d.seen }

// Step 喂入一个字节，返回 0、1 或 2 个事件。
// 产生事件后 Decoder 自动复位；第二个事件（若有）是重解析 b 的结果，
// 此时若返回 nil-event（重解析后 b 又成为新前缀），InProgress 为 true。
func (d *Decoder) Step(b byte) []Event {
	if d.need == 0 {
		switch {
		case b < 0x80:
			return []Event{{R: rune(b), Kind: Scalar, Len: 1}}
		case b >= 0xC2 && b <= 0xDF:
			d.lead, d.r, d.need, d.seen = b, rune(b&0x1F), 1, 1
		case b == 0xE0, b >= 0xE1 && b <= 0xEF:
			d.lead, d.r, d.need, d.seen = b, rune(b&0x0F), 2, 1
		case b == 0xF0, b >= 0xF1 && b <= 0xF3, b == 0xF4:
			d.lead, d.r, d.need, d.seen = b, rune(b&0x07), 3, 1
		default: // 80..BF 孤立续字节、C0 C1 F5..FF 恒非法
			return []Event{{Kind: Bad, Len: 1}}
		}
		return nil
	}
	ok := cont(b)
	if d.seen == 1 {
		switch d.lead {
		case 0xE0:
			ok = b >= 0xA0 && b <= 0xBF
		case 0xED:
			ok = b >= 0x80 && b <= 0x9F
		case 0xF0:
			ok = b >= 0x90 && b <= 0xBF
		case 0xF4:
			ok = b >= 0x80 && b <= 0x8F
		}
	}
	if !ok {
		n := d.seen
		d.lead, d.r, d.need, d.seen = 0, 0, 0, 0
		es := []Event{{Kind: Bad, Len: n}}
		if es2 := d.Step(b); es2 != nil {
			es = append(es, es2...)
		}
		return es
	}
	d.r = d.r<<6 | rune(b&0x3F)
	d.need--
	d.seen++
	if d.need == 0 {
		r, n := d.r, d.seen
		d.lead, d.r, d.need, d.seen = 0, 0, 0, 0
		return []Event{{R: r, Kind: Scalar, Len: n}}
	}
	return nil
}

// Flush 在流结束时调用：残留合法前缀返回一个 Bad 事件，否则 nil。
func (d *Decoder) Flush() *Event {
	if d.seen > 0 {
		n := d.seen
		d.lead, d.r, d.need, d.seen = 0, 0, 0, 0
		return &Event{Kind: Bad, Len: n}
	}
	return nil
}

// Encode 把标量值 r 编码成 UTF-8 字节。
func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12)&0x3F, 0x80 | byte(r>>6)&0x3F, 0x80 | byte(r&0x3F)}
	}
}

// Cont 报告 b 是否为续字节 80..BF。
func Cont(b byte) bool { return cont(b) }

// LeadLen 报告首字节 lead 所启序列的总长度；非首字节返回 0。
func LeadLen(lead byte) int {
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		return 2
	case lead >= 0xE0 && lead <= 0xEF:
		return 3
	case lead >= 0xF0 && lead <= 0xF4:
		return 4
	default:
		return 0
	}
}
