// Package handle 定义句柄：表标签、槽位号与代号的编解码及相等判定。
package handle

// 位宽划分：tag(16) | index(28) | gen(20)，见 DESIGN.md。
const (
	GenBits   = 20
	IndexBits = 28
	TagBits   = 16

	// MaxGen 是代号上限，递进到该值后槽位耗尽退役，代号永不回绕。
	MaxGen = 1<<GenBits - 1
	// MaxIndex 是可寻址的最大槽位号。
	MaxIndex = 1<<IndexBits - 1
)

// Handle 是对象的对外句柄，零值永不有效。
type Handle uint64

// Zero 是零值句柄，任何表都不承认它。
const Zero Handle = 0

// New 把表标签、槽位号、代号编码为一个句柄。
func New(tag uint16, index, gen uint32) Handle {
	return Handle(uint64(tag)<<(IndexBits+GenBits) |
		uint64(index&MaxIndex)<<GenBits | uint64(gen&MaxGen))
}

// Tag 返回句柄所属表的标签。
func (h Handle) Tag() uint16 { return uint16(uint64(h) >> (IndexBits + GenBits)) }

// Index 返回句柄指向的槽位号。
func (h Handle) Index() uint32 { return uint32(uint64(h) >> GenBits & MaxIndex) }

// Gen 返回句柄携带的代号。
func (h Handle) Gen() uint32 { return uint32(uint64(h) & MaxGen) }

// IsZero 报告句柄是否为零值。
func (h Handle) IsZero() bool { return h == Zero }

// Equal 判定两个句柄是否逐位相同。
func (h Handle) Equal(o Handle) bool { return h == o }
