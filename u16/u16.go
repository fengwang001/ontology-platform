// Package u16 做手写的 UTF-16LE/UTF-16BE 流式解码与编码。
package u16

import "ontology/scalar"

// Order 是字节序。
type Order uint8

const (
	LE Order = iota
	BE
)

// Kind 与 u8.Kind 同义（OK/Bad/Trunc）。
type Kind = u8Kind

type u8Kind uint8

// Event 是一次 UTF-16 解码结果。
type Event struct {
	Kind u8Kind
	R    scalar.Scalar
	Size int // 吞掉的输入字节数（2 或 4；Trunc 高代理为 2、奇字节为 1）
}

// Decoder 跨 Write 续接半个代码单元。
type Decoder struct {
	scalar.Checks
	order Order
	set   bool // 字节序是否已显式确定
	half  byte // 半个代码单元
	have  bool
	hi    rune // 待配对高代理
	hiOK  bool
}

// NewDecoder 以指定字节序构造；set=false 且首两字节为 BOM 时自动定序。
func NewDecoder(o Order, set bool) *Decoder { return &Decoder{order: o, set: set} }

// Feed 送入字节，返回事件；ok=false 表示需等待。
func (d *Decoder) Feed(b byte) (e Event, ok bool) { return Event{}, false }

// EOF 报告残留：奇字节或孤立高代理均为 Trunc。
func (d *Decoder) EOF() (e Event, ok bool) { return Event{}, false }

// Pending/Held 同 u8（缓存上限 1 字节，另有一个高代理代码单元）。
func (d *Decoder) Pending() []byte { return nil }
func (d *Decoder) Held() int       { return 0 }

// Order 返回已确定的字节序（供 BOM 嗅探）。
func (d *Decoder) Order() Order { return d.order }

// Encode 把标量编码为指定字节序的 UTF-16（含代理对；非法标量按 U+FFFD）。
func Encode(r scalar.Scalar, o Order) []byte { return nil }
