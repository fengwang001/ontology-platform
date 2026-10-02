// Package frame 实现一个类 Snappy 分帧格式的分块流式写出器与校验读取器，
// 内部使用字节游程压缩，并按压缩收益与历史失败自适应决定是否压缩每个块。
package frame

import (
	"errors"
	"fmt"
	"hash/crc32"
)

// 块类型。
const (
	chunkCompressed   = 0x00 // 游程压缩数据块
	chunkRaw          = 0x01 // 原样数据块
	chunkIdentifier   = 0xFF // 标识块
	identifierPayload = "sNaPpY"
)

// 限制。
const (
	maxUncompressed = 65536                  // 每块未压缩数据上限
	maxPayload      = 65540                  // 数据块负载上限（4 字节校验和 + 65536）
	headerLen       = 4                      // 1 字节类型 + 3 字节小端长度
	maxBuffered     = headerLen + maxPayload // 读取端缓冲上限
	minCompressTry  = 16                     // 小于该长度的块不尝试压缩
)

// 哨兵错误，*FrameError 可通过 errors.Is 匹配到下列种类。
var (
	ErrParam         = errors.New("frame: invalid parameter")
	ErrClosed        = errors.New("frame: closed")
	ErrNoIdentifier  = errors.New("frame: missing identifier chunk")
	ErrBadIdentifier = errors.New("frame: bad identifier chunk")
	ErrReserved      = errors.New("frame: reserved unskippable chunk type")
	ErrChunkLen      = errors.New("frame: invalid chunk payload length")
	ErrDecode        = errors.New("frame: corrupt compressed payload")
	ErrChecksum      = errors.New("frame: checksum mismatch")
	ErrTruncated     = errors.New("frame: truncated stream")
)

// FrameError 携带出错块头第一个字节在整个流中的偏移。
type FrameError struct {
	Offset int64
	Err    error
}

func (e *FrameError) Error() string {
	return fmt.Sprintf("frame: offset %d: %v", e.Offset, e.Err)
}

func (e *FrameError) Unwrap() error { return e.Err }

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// maskedChecksum 计算未压缩数据的掩码校验和：
// ((crc>>15) | (crc<<17)) + 0xa282ead8（uint32 回绕）。
func maskedChecksum(data []byte) uint32 {
	crc := crc32.Checksum(data, castagnoli)
	return ((crc >> 15) | (crc << 17)) + 0xa282ead8
}

// identifierChunk 是完整的标识块字节序列。
var identifierChunk = []byte{
	chunkIdentifier, 6, 0, 0, 's', 'N', 'a', 'P', 'p', 'Y',
}

// isSkippable 报告类型是否为可跳过块（0x80 到 0xFE）。
func isSkippable(t byte) bool { return t >= 0x80 && t != chunkIdentifier }

// appendHeader 向 dst 追加块头。
func appendHeader(dst []byte, typ byte, payloadLen int) []byte {
	return append(dst, typ,
		byte(payloadLen), byte(payloadLen>>8), byte(payloadLen>>16))
}
