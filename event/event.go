// Package event 定义事件（序号 + 载荷）及其编解码。
package event

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrEventTruncated 表示事件体字节不足，无法解码。
var ErrEventTruncated = errors.New("event: truncated event body")

// HeaderSize 是事件体中序号字段的字节数。
const HeaderSize = 8

// Event 是一条只追加日志事件：全局递增的序号 + 任意载荷（可为空）。
type Event struct {
	Seq     uint64
	Payload []byte
}

// Size 返回事件编码后的字节数。
func (e Event) Size() int { return HeaderSize + len(e.Payload) }

// Encode 把事件编码为 seq(8, 大端) + payload。
func Encode(e Event) []byte {
	buf := make([]byte, e.Size())
	binary.BigEndian.PutUint64(buf, e.Seq)
	copy(buf[HeaderSize:], e.Payload)
	return buf
}

// Decode 从字节串解码事件；字节不足时返回 ErrEventTruncated。
func Decode(buf []byte) (Event, error) {
	if len(buf) < HeaderSize {
		return Event{}, fmt.Errorf("%w: got %d bytes", ErrEventTruncated, len(buf))
	}
	payload := make([]byte, len(buf)-HeaderSize)
	copy(payload, buf[HeaderSize:])
	return Event{Seq: binary.BigEndian.Uint64(buf), Payload: payload}, nil
}
