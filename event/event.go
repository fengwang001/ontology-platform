// Package event 定义日志事件及其长度前缀 + CRC32 的帧编解码。
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// 可经 errors.Is 区分的损坏/语义错误，被 segment、replay、repair 复用。
var (
	ErrHeader = errors.New("event: segment header incomplete or corrupt")
	ErrLength = errors.New("event: length prefix incomplete or invalid")
	ErrBody   = errors.New("event: event payload incomplete")
	ErrCRC    = errors.New("event: crc mismatch")
	ErrRange  = errors.New("event: invalid range (from > to)")
	ErrGap    = errors.New("event: non-contiguous sequence gap between segments")
	ErrIndex  = errors.New("event: sparse index stale, fell back to full scan")
)

const (
	LenSize       = 4 // length 前缀字节数
	CRCSize       = 4 // 帧尾 CRC 字节数
	FrameOverhead = LenSize + CRCSize
	MaxPayload    = 1<<32 - 1
)

// Event 是一条全局序号 + 任意（允许空）载荷的事件。
type Event struct {
	Seq     uint64
	Payload []byte
}

// FrameLen 返回一条载荷在磁盘上所占帧字节数。
func FrameLen(payload []byte) int {
	return FrameOverhead + len(payload)
}

// EncodeFrame 把载荷编码成自校验帧：length || payload || crc32(length||payload)。
func EncodeFrame(dst, payload []byte) []byte {
	var head [LenSize]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(payload)))

	dst = append(dst, head[:]...)
	dst = append(dst, payload...)

	h := crc32.NewIEEE()
	h.Write(head[:])
	h.Write(payload)
	var c [CRCSize]byte
	binary.BigEndian.PutUint32(c[:], h.Sum32())
	return append(dst, c[:]...)
}

// DecodeFrameAt 从 buf 的起点解码一帧。
// 返回载荷长度；失败按截断位置包装对应哨兵错误。
func DecodeFrameAt(buf []byte) (int, error) {
	if len(buf) < LenSize {
		return 0, ErrLength
	}
	n := int(binary.BigEndian.Uint32(buf[:LenSize]))
	end := LenSize + n
	if len(buf) < end {
		return 0, ErrBody
	}
	if len(buf) < end+CRCSize {
		return 0, ErrCRC
	}

	h := crc32.NewIEEE()
	h.Write(buf[:end])
	if binary.BigEndian.Uint32(buf[end:end+CRCSize]) != h.Sum32() {
		return 0, ErrCRC
	}
	return n, nil
}

// Payload 返回已验证帧中载荷的拷贝，避免持有底层读缓冲。
func Payload(frame []byte) []byte {
	n := int(binary.BigEndian.Uint32(frame[:LenSize]))
	out := make([]byte, n)
	copy(out, frame[LenSize:LenSize+n])
	return out
}
