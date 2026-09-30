package neighbor

import "errors"

// 拒绝原因，按规范固定顺序判定，只返回第一个。
var (
	ErrClockBackward  = errors.New("neighbor: clock moved backwards")
	ErrEmptyAddress   = errors.New("neighbor: empty network address")
	ErrEmptyLinkLayer = errors.New("neighbor: empty link-layer address")
	ErrNoEntry        = errors.New("neighbor: no entry for address")
	ErrEntryLimit     = errors.New("neighbor: entry limit reached")
)
