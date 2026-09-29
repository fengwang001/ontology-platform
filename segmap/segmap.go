package segmap

import (
	"errors"
	"sync"
)

// 错误原因：彼此可通过 errors.Is 区分；被拒绝的操作不产生任何副作用。
var (
	// ErrInvalidRange 区间为空、端点颠倒，或为负数。
	ErrInvalidRange = errors.New("segmap: invalid or empty range")
	// ErrFileTooLarge 区间超过文件长度上限。
	ErrFileTooLarge = errors.New("segmap: range exceeds max file length")
	// ErrSameFileOverlap 同一文件内克隆的源区间与目标区间相互重叠。
	ErrSameFileOverlap = errors.New("segmap: clone source and destination ranges overlap within same file")
	// ErrSpaceExhausted 物理空间不足，无法容纳请求的字节数。
	ErrSpaceExhausted = errors.New("segmap: physical space exhausted")
)

// MaxFileLength 是单个文件允许的最大逻辑长度（偏移上界）。
const MaxFileLength = int64(1) << 40

// extent 是一段左闭右开逻辑区间到物理空间起点的映射。
// 逻辑与物理空间均连续，因此 [logicalStart, logicalStart+length)
// 映射到 [physicalStart, physicalStart+length)。
type extent struct {
	logicalStart  int64
	physicalStart int64
	length        int64
}

func (e extent) logicalEnd() int64 { return e.logicalStart + e.length }

// logger 记录每个操作的输入、输出与判定依据。
type logger interface {
	logf(format string, args ...any)
}

// file 是单个逻辑文件：有序、互不重叠且已合并的区段列表与逻辑长度。
type file struct {
	mu      sync.RWMutex
	name    string
	length  int64
	extents []extent
}
