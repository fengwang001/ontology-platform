// Package u8 是手写的 UTF-8 逐标量解码器/编码器，依赖 scalar。
package u8

import "ontology/scalar"

// Kind 标识一次解码结果。
type Kind uint8

const (
	// Rune 为合法标量；Invalid 为非法单元；Trunc 为 EOF 截断前缀。
	Rune Kind = iota
	Invalid
	Trunc
)

// Event 是一次解码产出的单元。At 为首字节的全局偏移，Len 为吞掉字节数。
type Event struct {
	Kind Kind
	R    rune
	At   int
	Len  int
}

// MaxPending 是合法前缀缓存的硬上限（4 字节字符最多缓存 3 字节）。
const MaxPending = 3

const (
	bom0, bom1, bom2 = 0xEF, 0xBB, 0xBF
	runeError        = '\uFFFD'
)

// Decoder 是逐字节 UTF-8 状态机。单次实例非并发安全。
type Decoder struct {
	at     int // 总已喂入字节数（下个字节偏移）
	unitAt int // 当前单元首字节偏移
	pend   []byte
	need   int // 当前首字节期望的总长度
	checks int64
}

// Checks 返回字节被检查的总次数。
func (d *Decoder) Checks() int64 { return d.checks }

// IsCont 判断 b 是否为续字节（导出给 par 对齐用）。
func IsCont(b byte) bool { return b&0xC0 == 0x80 }

// Reset 清空状态。
func (d *Decoder) Reset() { d.at, d.unitAt, d.pend, d.need, d.checks = 0, 0, nil, 0, 0 }

// Feed 喂入字节，产出 0 或 1 个单元。
func (d *Decoder) Feed(b byte) (Event, bool) { return Event{}, false }

// Finish 在流结束时返回残留前缀单元（无残留则 ok=false）。
func (d *Decoder) Finish() (Event, bool) { return Event{}, false }

// EncodeLen 返回标量的 UTF-8 编码字节数。
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

// Encode 把合法标量追加到 dst，非法标量写 U+FFFD。
func Encode(dst []byte, r rune) []byte {
	if !scalar.Valid(r) {
		r = runeError
	}
	switch EncodeLen(r) {
	case 1:
		return append(dst, byte(r))
	case 2:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case 3:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F, 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}

// BOMPrefixAt 判断 buf 是否恰以 UTF-8 BOM 开头。
func BOMPrefixAt(buf []byte) bool {
	return len(buf) >= 3 && buf[0] == bom0 && buf[1] == bom1 && buf[2] == bom2
}
