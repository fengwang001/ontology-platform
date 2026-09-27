// Package u16 是手写的 UTF-16LE/BE 解码器/编码器，依赖 scalar。
package u16

import "ontology/scalar"

// Order 为字节序。
type Order uint8

const (
	// LE 小端，BE 大端，Auto 仅用于流首 BOM 自动探测（默认 LE）。
	LE Order = iota
	BE
	Auto
)

// Kind 同 u8.Kind：Rune / Invalid / Trunc。
type Kind uint8

const (
	Rune Kind = iota
	Invalid
	Trunc
)

// Event 是一次解码单元。At 为首字节全局偏移，Len 为字节数（2 或 4）。
type Event struct {
	Kind Kind
	R    rune
	At   int
	Len  int
}

// MaxPending 为字节缓存硬上限（最多半个 code unit）。
const MaxPending = 1

const runeError = '\uFFFD'

// Decoder 是流式 UTF-16 状态机。
type Decoder struct {
	order    Order
	resolved bool // 字节序是否已最终确定
	at       int
	pend     bool // 缓存了半个 code unit
	half     byte
	hi       uint16
	hiAt     int
	hiOn     bool
	checks   int64
}

// NewDecoder 创建解码器。
func NewDecoder(o Order) *Decoder { return &Decoder{order: o} }

// Checks 返回字节检查次数。
func (d *Decoder) Checks() int64 { return d.checks }

// Order 返回最终字节序（BOM 后可用）。
func (d *Decoder) Order() Order { return d.order }

// Reset 清空状态。
func (d *Decoder) Reset() { *d = Decoder{order: d.order} }

// Feed 喂入字节；ok=true 时 ev 为 Rune/Invalid（Trunc 只在 Finish 出现）。
func (d *Decoder) Feed(b byte) (ev Event, ok bool) { return Event{}, false }

// Finish 处理 EOF：奇数字节或残留高代理返回 Trunc。
func (d *Decoder) Finish() (Event, bool) { return Event{}, false }

func (d *Decoder) unit(lo, hi byte) (uint16, bool) {
	d.checks++
	if d.resolved || d.order != Auto {
		d.resolved = true
	}
	if d.order == BE {
		return uint16(lo)<<8 | uint16(hi), true
	}
	return uint16(hi) | uint16(lo)<<8, true
}

// Encode 把标量按字节序追加到 dst；辅助平面写代理对，非法标量写替换符。
func Encode(dst []byte, r rune, o Order) []byte {
	if !scalar.Valid(r) {
		r = runeError
	}
	put := func(u uint16) {
		if o == BE {
			dst = append(dst, byte(u>>8), byte(u))
		} else {
			dst = append(dst, byte(u), byte(u>>8))
		}
	}
	if r >= 0x10000 {
		hi, lo := scalar.Surrogates(r)
		put(hi)
		put(lo)
	} else {
		put(uint16(r))
	}
	return dst
}

// EncodeLen 返回 UTF-16 编码的 code unit 数（2 或 4 字节）。
func EncodeLen(r rune) int {
	if !scalar.Valid(r) {
		r = runeError
	}
	if r >= 0x10000 {
		return 4
	}
	return 2
}
