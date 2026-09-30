package extent

import "errors"

// 操作被拒绝时的可区分原因。所有被拒绝的操作保证不改变任何映射、
// 引用计数或已分配空间。
var (
	// ErrEmptyRange 区间为空或颠倒（长度 <= 0）。
	ErrEmptyRange = errors.New("extent: empty or inverted range")
	// ErrOutOfBounds 区间超出文件长度上限。
	ErrOutOfBounds = errors.New("extent: range exceeds file length limit")
	// ErrOverlap 同一文件内克隆的源与目标区间重叠。
	ErrOverlap = errors.New("extent: clone source and destination overlap in the same file")
	// ErrNoSpace 物理空间不足，无法容纳整个新区间。
	ErrNoSpace = errors.New("extent: insufficient physical space")
	// ErrUnknownFile 文件未注册到卷。
	ErrUnknownFile = errors.New("extent: unknown file")
)
