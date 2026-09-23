// Package handle 定义对外暴露的句柄：槽号、代号与表标的 64 位编解码。
package handle

import "fmt"

// Handle 是不透明的 64 位句柄。位布局（低到高）：
// 槽号 20 位 | 表标 16 位 | 代号 28 位。
type Handle uint64

const (
	indexBits = 20
	tagBits   = 16
	genBits   = 28
	// IndexMax 是槽号位能表示的最大槽号。
	IndexMax = 1<<indexBits - 1
	tagMax   = 1<<tagBits - 1
	// GenMax 是代号字段上限（与 slot.MaxGeneration 一致）。
	GenMax = uint32(1<<genBits - 1)

	tagShift = indexBits
	genShift = indexBits + tagBits
)

// Zero 是零值句柄，永远无效。
const Zero Handle = 0

// Pack 把槽号、表标、代号编码成句柄。
func Pack(index int, tag uint16, gen uint32) Handle {
	return Handle(uint64(uint64(index)) | uint64(tag)<<tagShift | uint64(gen)<<genShift)
}

// Index 返回槽号。
func (h Handle) Index() int { return int(h & (1<<indexBits - 1)) }

// Tag 返回表标。
func (h Handle) Tag() uint16 { return uint16((h >> tagShift) & (1<<tagBits - 1)) }

// Generation 返回代号；代号为 0 即零值句柄。
func (h Handle) Generation() uint32 { return uint32(h >> genShift) }

// IsZero 判断是否零值句柄。
func (h Handle) IsZero() bool { return h == Zero }

// Equal 判定两句柄是否完全相同。
func (h Handle) Equal(other Handle) bool { return h == other }

// String 便于诊断输出。
func (h Handle) String() string {
	return fmt.Sprintf("handle{idx=%d tag=%d gen=%d}", h.Index(), h.Tag(), h.Generation())
}
