package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Event 是日志中的一条事件：单调递增序号 Seq 与任意字节载荷 Payload。
type Event struct {
	Seq     uint64
	Payload []byte
}

// MaxPayload 是单条载荷上限（length 前缀为 uint32）。
const MaxPayload = (1 << 31) - 1

// ErrPayloadTooLarge 在载荷超过 uint32 长度前缀容量时返回。
var ErrPayloadTooLarge = errors.New("event: payload too large")

// EncodeFrame 将事件编码为帧：8 字节大端序号 + 载荷 + 4 字节 CRC32。
// 这里的 CRC 覆盖「序号 + 载荷」，使事件整体可自校验。
func EncodeFrame(e Event) ([]byte, error) {
	if len(e.Payload) > MaxPayload {
		return nil, ErrPayloadTooLarge
	}
	buf := make([]byte, 8+len(e.Payload)+4)
	binary.BigEndian.PutUint64(buf[:8], e.Seq)
	copy(buf[8:], e.Payload)
	crc := crc32.ChecksumIEEE(buf[:8+len(e.Payload)])
	binary.BigEndian.PutUint32(buf[8+len(e.Payload):], crc)
	return buf, nil
}

// DecodeFrame 解码一帧的完整字节，校验 CRC。
func DecodeFrame(buf []byte) (Event, error) {
	if len(buf) < 12 {
		return Event{}, errors.New("event: frame too short")
	}
	n := len(buf) - 4
	if crc32.ChecksumIEEE(buf[:n]) != binary.BigEndianUint32(buf[n:]) {
		return Event{}, errors.New("event: crc mismatch")
	}
	return Event{Seq: binary.BigEndian.Uint64(buf[:8]), Payload: append([]byte(nil), buf[8:n]...)}, nil
}
