package u8

import "ontology/scalar"

// EventKind 描述解码器一次产出。
type EventKind uint8

const (
	// EvScalar 是一个合法标量。
	EvScalar EventKind = iota
	// EvInvalid 是一个非法单元（长度=被吞掉的字节数）。
	EvInvalid
	// EvTruncated 是流末未完成但尚未遇到非法字节的前缀。
	EvTruncated
)

// Event 是一次解码产出。
type Event struct {
	Kind EventKind
	R    rune
	Len  int
}

// Decoder 是逐字节 UTF-8 状态机。Step 每消费一个字节，
// 返回一个事件或零事件（ok=false 表示仍在等待后续字节）。
// 非法序列的当前字节不会被消费：调用方需把它重新喂回。
type Decoder struct {
	need       int    // 还需续字节数
	total      int    // 本序列总长度
	val        rune   // 已积累的值
	lo, hi     rune   // 下一字节允许区间
	pending    []byte // 已挂起字节（供长度统计）
	truncValid bool   // 挂起前缀是否目前仍合法
}

// PendingLen 返回当前缓存的挂起字节数（硬上界 3）。
func (d *Decoder) PendingLen() int { return len(d.pending) }

// Step 喂入一个字节。
func (d *Decoder) Step(b byte) (Event, bool) {
	if d.need == 0 {
		return d.start(b)
	}
	d.truncValid = false
	if b < byte(d.lo) || b > byte(d.hi) {
		e := Event{Kind: EvInvalid, Len: len(d.pending)}
		d.reset()
		return e, true // 当前字节未消费
	}
	d.val = d.val<<6 | rune(b&0x3F)
	d.pending = append(d.pending, b)
	d.need--
	d.lo, d.hi = 0x80, 0xBF
	if d.need == 0 {
		e := Event{Kind: EvScalar, R: d.val, Len: d.total}
		d.reset()
		return e, true
	}
	d.truncValid = true
	return Event{}, false
}

func (d *Decoder) start(b byte) (Event, bool) {
	switch {
	case b < 0x80:
		return Event{Kind: EvScalar, R: rune(b), Len: 1}, true
	case b < 0xC2: // 80..BF 续字节，C0 C1 非最短
		return Event{Kind: EvInvalid, Len: 1}, true
	case b < 0xE0:
		d.init(1, 2, rune(b&0x1F), 0x80, 0xBF, b)
	case b == 0xE0:
		d.init(2, 3, rune(b&0x0F), 0xA0, 0xBF, b)
	case b < 0xED:
		d.init(2, 3, rune(b&0x0F), 0x80, 0xBF, b)
	case b == 0xED:
		d.init(2, 3, rune(b&0x0F), 0x80, 0x9F, b)
	case b < 0xF0:
		d.init(2, 3, rune(b&0x0F), 0x80, 0xBF, b)
	case b == 0xF0:
		d.init(3, 4, rune(b&0x07), 0x90, 0xBF, b)
	case b < 0xF4:
		d.init(3, 4, rune(b&0x07), 0x80, 0xBF, b)
	case b == 0xF4:
		d.init(3, 4, rune(b&0x07), 0x80, 0x8F, b)
	default: // F5..FF
		return Event{Kind: EvInvalid, Len: 1}, true
	}
	d.truncValid = true
	return Event{}, false
}

func (d *Decoder) init(need, total int, val, lo, hi rune, b byte) {
	d.need, d.total = need, total
	d.val, d.lo, d.hi = val, lo, hi
	d.pending = append(d.pending[:0], b)
}

func (d *Decoder) reset() {
	d.need, d.total = 0, 0
	d.val, d.lo, d.hi = 0, 0, 0
	d.pending = d.pending[:0]
	d.truncValid = false
}

// Flush 在流结束时调用：返回未完成前缀事件。
func (d *Decoder) Flush() (Event, bool) {
	if d.need == 0 {
		return Event{}, false
	}
	k := EvTruncated
	if !d.truncValid {
		k = EvInvalid
	}
	e := Event{Kind: k, Len: len(d.pending)}
	d.reset()
	return e, true
}

// EncodeLen 返回编码标量所需字节数。
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

// Encode 把合法标量追加到 dst，手工拼字节，不用任何隐式转换。
func Encode(dst []byte, r rune) []byte {
	if !scalar.IsScalar(r) {
		r = scalar.ReplacementChar
	}
	switch {
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
