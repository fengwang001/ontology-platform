package overflow

import "errors"

// 可区分的拒绝/自检原因，调用方用 errors.Is 判定。
var (
	// ErrInvalidThreshold 阈值非法（<= 0）。
	ErrInvalidThreshold = errors.New("overflow: invalid threshold")
	// ErrInvalidMaxBlocks 溢出块数上限非法（<= 0）。
	ErrInvalidMaxBlocks = errors.New("overflow: invalid max blocks")
	// ErrEmptyKey 键为空。
	ErrEmptyKey = errors.New("overflow: empty key")
	// ErrBlockLimit 溢出块数超出上限。
	ErrBlockLimit = errors.New("overflow: overflow block limit exceeded")
	// ErrDanglingRef 主记录引用了不存在的溢出块。
	ErrDanglingRef = errors.New("overflow: dangling block reference")
	// ErrUnreferencedBlock 溢出块没有被任何主记录引用（或引用计数不为 1）。
	ErrUnreferencedBlock = errors.New("overflow: unreferenced overflow block")
)
