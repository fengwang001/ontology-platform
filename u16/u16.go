package u16

import "ontology/scalar"

// Order 表示 UTF-16 字节序。
type Order int

const (
	// Unset 表示字节序尚未由 BOM 确定（确定前按 LE 解释）。
	Unset Order = iota
	LE
	BE
)

// Event 是一次 UTF-16 解码产出。
type Event struct {
	Start int64
	Len   int
	R     rune
	Valid bool
	// Trunc 为 true 表示流结束时被截断的半个字符（区分于非法字节）。
	Trunc bool
	// BOM 为 true 表示这是位于整个流开头的 BOM。
	BOM bool
}

// MaxPending 是未决缓存硬上限：至多 1 个奇数字节 + 1 个高代理。
const MaxPending = 3

// Decoder 是有状态的 UTF-16 字节解码器，单个实例非并发安全。
type Decoder struct {
	order Order

	odd    byte
	hasOdd bool

	high     rune
	highAt   int64
	hasHigh  bool
	headUsed bool

	pos    int64
	Checks int64
}

// NewDecoder 以给定字节序构造解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o} }

// Order 返回当前实际字节序（Unset 视作 LE）。
func (d *Decoder) Order() Order {
	if d.order == Unset {
		return LE
	}
	return d.order
}

// Pos 返回已送入的字节流偏移。
func (d *Decoder) Pos() int64 { return d.pos }

// Pending 返回未决缓存字节数（奇数字节 1，孤立高代理 2）。
func (d *Decoder) Pending() int {
	n := 0
	if d.hasOdd {
		n++
	}
	if d.hasHigh {
		n += 2
	}
	return n
}

func (d *Decoder) unit(lo, hi byte) rune {
	if d.Order() == BE {
		return rune(lo)<<8 | rune(hi)
	}
	return rune(hi)<<8 | rune(lo)
}

func (d *Decoder) push(u rune, at int64, emit func(Event) bool) bool {
	if !d.headUsed {
		d.headUsed = true
		if u == scalar.BOM {
			if d.order == Unset {
				d.order = LE
			}
			return emit(Event{Start: at, Len: 2, R: scalar.BOM, Valid: true, BOM: true})
		}
	}
	if d.hasHigh {
		if scalar.IsLowSurrogate(u) {
			d.hasHigh = false
			return emit(Event{Start: d.highAt, Len: 4,
				R: scalar.SurrogatePair(d.high, u), Valid: true})
		}
		atHigh := d.highAt
		d.hasHigh = false
		if !emit(Event{Start: atHigh, Len: 2, R: scalar.Replacement, Valid: false}) {
			return false
		}
		// 当前码元作为新字符重新处理（高代理不吞掉它）。
	}
	switch {
	case scalar.IsScalar(u):
		return emit(Event{Start: at, Len: 2, R: u, Valid: true})
	case scalar.IsHighSurrogate(u):
		d.high, d.highAt, d.hasHigh = u, at, true
		return true
	default: // 孤立低代理
		return emit(Event{Start: at, Len: 2, R: scalar.Replacement, Valid: false})
	}
}

func (d *Decoder) takeUnit(first, second byte, at int64, emit func(Event) bool) bool {
	d.Checks++
	// 流首 BOM 负责确定字节序：先按当前序解释，若识别出 FEFF 则锁定。
	if !d.headUsed {
		be := rune(first)<<8 | rune(second)
		le := rune(second)<<8 | rune(first)
		if d.order != BE && le == scalar.BOM {
			d.order = LE
		} else if d.order != LE && be == scalar.BOM {
			d.order = BE
		}
	}
	return d.push(d.unit(first, second), at, emit)
}

// Decode 喂入一块字节，每产出一个标量/非法单元调用一次 emit。
func (d *Decoder) Decode(p []byte, emit func(Event) bool) {
	i := 0
	if d.hasOdd && len(p) > 0 {
		if !d.takeUnit(d.odd, p[0], d.pos-1, emit) {
			return
		}
		d.hasOdd = false
		i = 1
		d.pos++
	}
	for ; i+1 < len(p); i += 2 {
		if !d.takeUnit(p[i], p[i+1], d.pos+int64(i), emit) {
			return
		}
	}
	if i < len(p) {
		d.odd = p[i]
		d.hasOdd = true
		i++
	}
	d.pos += int64(i)
}

// Flush 在流结束时返回残留：奇数字节或孤立高代理，均为截断。
func (d *Decoder) Flush() *Event {
	if d.hasOdd {
		d.hasOdd = false
		return &Event{Start: d.pos - 1, Len: 1, R: scalar.Replacement, Trunc: true}
	}
	if d.hasHigh {
		d.hasHigh = false
		return &Event{Start: d.highAt, Len: 2, R: scalar.Replacement, Trunc: true}
	}
	return nil
}

// Encode 把标量编码为给定字节序的 UTF-16，追加到 dst。
func Encode(dst []byte, r rune, o Order) []byte {
	put := func(u rune) []byte {
		if o == BE {
			return append(dst, byte(u>>8), byte(u))
		}
		return append(dst, byte(u), byte(u>>8))
	}
	if r >= 0x10000 {
		hi, lo := scalar.SplitSurrogate(r)
		dst = put(hi)
		return put(lo)
	}
	return put(r)
}

// EncodedLen 返回标量占用的 UTF-16 字节数（2 或 4）。
func EncodedLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}
