// Package event 定义日志事件及其长度前缀 + CRC32 记录编解码。
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Event 是日志中的一条事件：单调序号加不透明载荷（允许为空）。
type Event struct {
	Seq     uint64
	Payload []byte
}

// ErrCRCMismatch 表示记录的 CRC32 校验失败（字节已被改动）。
var ErrCRCMismatch = errors.New("event: crc mismatch")

// ErrFrameTruncated 表示给定帧短于长度前缀所声明的完整记录长度。
var ErrFrameTruncated = errors.New("event: frame shorter than declared length")

// FrameLen 返回一条事件编码后的总字节数：4 长度前缀 + payload + 4 CRC。
func FrameLen(payloadLen int) int { return 4 + payloadLen + 4 }

// EncodeFrame 把载荷编码为一条自校验记录，追加到 dst 后返回。
func EncodeFrame(dst, payload []byte) []byte {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	dst = append(dst, lenBuf[:]...)
	dst = append(dst, payload...)
	var crcBuf [4]byte
	crc := crc32.ChecksumIEEE(append(lenBuf[:], payload...))
	binary.BigEndian.PutUint32(crcBuf[:], crc)
	return append(dst, crcBuf[:]...)
}

// ValidateFrame 校验一条完整记录（长度前缀 + 载荷 + CRC）。
// 合法时返回载荷长度；CRC 不符返回 ErrCRCMismatch。
func ValidateFrame(frame []byte) (int, error) {
	payloadLen := int(binary.BigEndian.Uint32(frame[:4]))
	if len(frame) < FrameLen(payloadLen) {
		return 0, ErrFrameTruncated
	}
	if crc32.ChecksumIEEE(frame[:4+payloadLen]) != binary.BigEndian.Uint32(frame[4+payloadLen:]) {
		return 0, ErrCRCMismatch
	}
	return payloadLen, nil
}
