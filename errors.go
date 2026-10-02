package ontology

import (
	"errors"
	"fmt"
)

// 分帧读取过程中可能出现的错误种类。所有种类均为 sentinel error，
// 可用 errors.Is 与带偏移的 *FrameError 比较。
var (
	// ErrParam 表示 NewWriter 的参数非法（F<1 或 S<1）。
	ErrParam = errors.New("frame: invalid parameter")
	// ErrClosed 表示 Writer 已 Close 后仍被调用。
	ErrClosed = errors.New("frame: writer closed")

	// ErrNoIdentifier 表示流不以标识块开头（或空流在 Close 时仍为空）。
	ErrNoIdentifier = errors.New("frame: missing stream identifier")
	// ErrBadIdentifier 表示标识块长度不是 6 或负载不是 "sNaPpY"。
	ErrBadIdentifier = errors.New("frame: bad stream identifier")
	// ErrReserved 表示出现 0x02..0x7F 的保留不可跳过块类型。
	ErrReserved = errors.New("frame: reserved unskippable chunk type")
	// ErrChunkLen 表示数据块负载长度小于 4 或大于 65540。
	ErrChunkLen = errors.New("frame: invalid chunk length")
	// ErrDecode 表示游程负载令牌截断，或解出数据超过 65536 字节。
	ErrDecode = errors.New("frame: run-length decode error")
	// ErrChecksum 表示掩码 CRC-32C 校验失败。
	ErrChecksum = errors.New("frame: checksum mismatch")
	// ErrTruncated 表示 Close 时停在块头或负载中间。
	ErrTruncated = errors.New("frame: truncated stream")
)

// FrameError 携带出错块头第一个字节在整个输入流中的字节偏移，
// Offset==0 用于空流缺少标识块的情形。
type FrameError struct {
	Offset int64
	Err    error
}

func (e *FrameError) Error() string {
	return fmt.Sprintf("frame error at offset %d: %v", e.Offset, e.Err)
}

func (e *FrameError) Unwrap() error { return e.Err }
