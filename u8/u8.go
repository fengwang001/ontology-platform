// Package u8 做手写的流式 UTF-8 逐标量解码与编码。
package u8

import "ontology/scalar"

// Kind 标识一次解码事件的种类。
type Kind uint8

const (
	OK     Kind = iota // 合法标量
	Bad                // 一个非法单元
	Trunc              // 流结束时残留的未完成合法前缀
)

// Event 是一次解码结果。
type Event struct {
	Kind Kind
	R    scalar.Scalar // OK 时为标量；Bad/Trunc 时为 U+FFFD
	Size int           // 本单元吞掉的字节数（Trunc 为残留前缀长度）
}

// Decoder 是有状态的 UTF-8 解码器，可跨 Write 续接半个字符。
type Decoder struct {
	scalar.Checks
	prefix [3]byte
	have   int
	need   int
	second bool
}

// Feed 送入至多一个字节 b，返回 0 个或 1 个事件（ok=false 表示需等待更多字节）。
func (d *Decoder) Feed(b byte) (e Event, ok bool) { return Event{}, false }

// EOF 报告流结束；若有残留前缀则返回 Trunc 事件。
func (d *Decoder) EOF() (e Event, ok bool) { return Event{}, false }

// Pending 返回当前缓存的待续前缀（长度 0..3）。
func (d *Decoder) Pending() []byte { return d.prefix[:d.have] }

// Held 返回当前缓存字节数。
func (d *Decoder) Held() int { return d.have }

// Encode 把标量编码为 UTF-8（非法标量按 U+FFFD）。
func Encode(r scalar.Scalar) []byte { return nil }
