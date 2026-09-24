// Package event 定义事件（序号 + 载荷）及其二进制编解码。
package event

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// HeaderLen 是事件编码的定长头长度：seq(8) + payloadLen(4)。
const HeaderLen = 12

// ErrTooShort 表示缓冲区不足以解出事件头。
var ErrTooShort = errors.New("event: buffer too short for header")

// ErrTruncated 表示事件体不完整。
var ErrTruncated = errors.New("event: truncated payload")

// Event 是一条只追加日志事件。
type Event struct {
	Seq     uint64
	Payload []byte
}

// EncodedLen 返回事件编码后的字节数。
func EncodedLen(payloadLen int) int { return HeaderLen + payloadLen }

// Encode 返回事件的二进制编码：seq u64le | payloadLen u32le | payload。
func Encode(e Event) []byte {
	buf := make([]byte, EncodedLen(len(e.Payload)))
	binary.LittleEndian.PutUint64(buf[0:8], e.Seq)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(e.Payload)))
	copy(buf[HeaderLen:], e.Payload)
	return buf
}

// Decode 从 buf 开头解出一个事件，返回事件与消耗的字节数。
func Decode(buf []byte) (Event, int, error) {
	if len(buf) < HeaderLen {
		return Event{}, 0, fmt.Errorf("%w: have %d need %d", ErrTooShort, len(buf), HeaderLen)
	}
	n := int(binary.LittleEndian.Uint32(buf[8:12]))
	if len(buf) < HeaderLen+n {
		return Event{}, 0, fmt.Errorf("%w: have %d need %d", ErrTruncated, len(buf)-HeaderLen, n)
	}
	e := Event{Seq: binary.LittleEndian.Uint64(buf[0:8])}
	if n > 0 {
		e.Payload = append([]byte(nil), buf[HeaderLen:HeaderLen+n]...)
	}
	return e, EncodedLen(n), nil
}
