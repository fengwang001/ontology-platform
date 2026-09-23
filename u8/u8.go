package u8

import "ontology/scalar"

// Event 描述一次解码产出：一个合法标量或一个非法单元。
// Start/Len 是该单元在整个输入字节流中的起始偏移与字节数。
type Event struct {
	Start int64
	Len   int
	R     rune
	Valid bool
	// BOM 为 true 表示这是位于整个流开头的 UTF-8 BOM。
	BOM bool
}

// MaxPending 是待决缓存的硬上限（含首字节，UTF-8 最长 4 字节）。
const MaxPending = 3

// Decoder 是有状态的字节级 UTF-8 解码器，可跨 Write 拼接。
// 单个实例非并发安全。
type Decoder struct {
	pend  [3]byte
	n     int
	need  int
	r     rune
	start int64
	pos   int64

	// Checks 统计字节被检查的总次数（非导出语义见 stream 测试）。
	Checks int64
	headUsed bool
}

// Pos 返回已经送入的字节流偏移。
func (d *Decoder) Pos() int64 { return d.pos }

// Pending 返回当前未决缓存字节数（<= MaxPending）。
func (d *Decoder) Pending() int { return d.n }

func (d *Decoder) emit(r rune, valid bool, start int64, n int) Event {
	return Event{Start: start, Len: n, R: r, Valid: valid}
}

// mark 负责识别「整个流开头」的 BOM：只标记真正的第一个产出事件。
func (d *Decoder) mark(ev Event) Event {
	if !d.headUsed {
		d.headUsed = true
		if ev.Valid && ev.R == scalar.BOM {
			ev.BOM = true
		}
	}
	return ev
}

func leadNeed(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	default:
		return 4
	}
}

// secondOK 判断首字节 lead 后第二字节 b 是否落在合法区间。
func secondOK(lead, b byte) bool {
	if b < 0x80 || b > 0xBF {
		return false
	}
	switch {
	case lead == 0xE0:
		return b >= 0xA0
	case lead == 0xED:
		return b <= 0x9F
	case lead == 0xF0:
		return b >= 0x90
	case lead == 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

func (d *Decoder) begin(b byte, at int64) (Event, bool) {
	switch {
	case b < 0x80:
		return d.mark(d.emit(rune(b), true, at, 1)), true
	case b < 0xC2: // 80..BF 孤立续字节；C0、C1 非最短
		return d.mark(d.emit(scalar.Replacement, false, at, 1)), true
	case b > 0xF4: // F5..FF 永不合法
		return d.mark(d.emit(scalar.Replacement, false, at, 1)), true
	default:
		d.pend[0] = b
		d.n = 1
		d.need = leadNeed(b)
		d.start = at
		d.r = rune(b & (0xFF >> d.need))
		return Event{}, false
	}
}

// Decode 喂入一块字节，每产出一个标量/非法单元调用一次 emit。
// emit 返回 false 可提前终止（用于输出上限）；调用方此时应进入终态。
func (d *Decoder) Decode(p []byte, emit func(Event) bool) {
	for i := 0; i < len(p); {
		at := d.pos
		b := p[i]
		d.Checks++
		i++
		d.pos++
		if d.n == 0 {
			if ev, ok := d.begin(b, at); ok {
				if !emit(ev) {
					return
				}
			}
			continue
		}
		cont := b >= 0x80 && b <= 0xBF
		pos := d.n // 该续字节在序列中的位置（1=第二字节）
		if cont && (pos > 1 || secondOK(d.pend[0], b)) {
			d.pend[d.n] = b
			d.n++
			d.r = d.r<<6 | rune(b&0x3F)
			if d.n == d.need {
				ev := d.mark(d.emit(d.r, scalar.IsScalar(d.r), d.start, d.n))
				d.n = 0
				if !emit(ev) {
					return
				}
			}
			continue
		}
		// 已缓存前缀构成一个非法单元；当前字节 b 留到下一轮重新解析。
		if !emit(d.mark(d.emit(scalar.Replacement, false, d.start, d.n))) {
			return
		}
		d.n = 0
		i--
		d.pos--
	}
}

// Flush 在流结束时调用：残留未收满序列是一个被截断的非法单元。
func (d *Decoder) Flush() *Event {
	if d.n == 0 {
		return nil
	}
	ev := d.mark(d.emit(scalar.Replacement, false, d.start, d.n))
	d.n = 0
	return &ev
}

// EncodedLen 返回标量的 UTF-8 编码字节数。
func EncodedLen(r rune) int {
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

// Encode 把合法标量编码成 UTF-8 字节，追加到 dst。
func Encode(dst []byte, r rune) []byte {
	switch r := r; {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
