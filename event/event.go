// Package event 定义日志事件及其长度前缀 + CRC32 帧编解码。
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Event 是一条带全局连续序号的事件；载荷允许为空字节串。
type Event struct {
	Seq     uint64
	Payload []byte
}

var crcTable = crc32.MakeTable(crc32.IEEE)

// ErrCRCMismatch 表示帧完整但载荷 CRC32 校验失败（内容被篡改）。
var ErrCRCMismatch = errors.New("event: crc mismatch")

// ErrPayloadTooLarge 表示载荷长度超出 uint32 可表达范围。
var ErrPayloadTooLarge = errors.New("event: payload too large")

// MaxPayload 是单条事件载荷的最大字节数。
const MaxPayload = (1 << 31) - 1

// EncodedLen 返回帧编码长度：4 字节长度 + 载荷 + 4 字节 CRC。
func EncodedLen(payload []byte) int {
	return 4 + len(payload) + 4
}

// Encode 将载荷追加编码到 dst，返回新切片。空载荷合法。
func Encode(dst, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, ErrPayloadTooLarge
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(payload)))
	dst = append(dst, head[:]...)
	dst = append(dst, payload...)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc32.Checksum(payload, crcTable))
	dst = append(dst, sum[:]...)
	return dst, nil
}

// Consume 从 buf 开头解析一帧，返回载荷（复制，不与 buf 共享底层数组）、
// 帧总长与错误。buf 内容不足以构成完整帧时返回错误（由调用方按尾余分类）。
func Consume(buf []byte) (payload []byte, n int, err error) {
	if len(buf) < 4 {
		return nil, 0, ErrShortLength
	}
	length := int(binary.BigEndian.Uint32(buf[:4]))
	frameLen := 4 + length + 4
	if len(buf) < frameLen {
		return nil, frameLen, ErrShortFrame
	}
	body := buf[4 : 4+length]
	want := binary.BigEndian.Uint32(buf[4+length:])
	if crc32.Checksum(body, crcTable) != want {
		return nil, frameLen, ErrCRCMismatch
	}
	payload = append(payload, body...)
	return payload, frameLen, nil
}

// 帧解析时的尾部不足错误，供上层细分截断类别。
var (
	// ErrShortLength 表示长度前缀不足 4 字节。
	ErrShortLength = errors.New("event: truncated length prefix")
	// ErrShortFrame 表示声明的帧体或 CRC 尚未写完整。
	ErrShortFrame = errors.New("event: truncated frame body or crc")
)
