// Package event 定义日志事件（序号 + 载荷）及其二进制编解码。
// 编码固定为小端 seq(8) + payload，载荷允许为空字节串。
package event

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Event 是一条只追加事件。
type Event struct {
	Seq     uint64
	Payload []byte
}

// Size 返回事件编码后的字节数。
func (e Event) Size() int { return 8 + len(e.Payload) }

// Encode 把事件写入 dst；dst 容量不足时返回错误。
func (e Event) Encode(dst []byte) error {
	if len(dst) < e.Size() {
		return fmt.Errorf("event: dst too small: have %d need %d", len(dst), e.Size())
	}
	binary.LittleEndian.PutUint64(dst[:8], e.Seq)
	copy(dst[8:], e.Payload)
	return nil
}

// AppendTo 返回追加了事件编码的 b。
func (e Event) AppendTo(b []byte) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], e.Seq)
	b = append(b, buf[:]...)
	return append(b, e.Payload...)
}

// ErrShortBody 表示事件体字节不足以解码出完整事件。
var ErrShortBody = errors.New("event: short body")

// Decode 从 src 解码单个事件，返回事件与消耗字节数。
func Decode(src []byte) (Event, int, error) {
	if len(src) < 8 {
		return Event{}, 0, ErrShortBody
	}
	seq := binary.LittleEndian.Uint64(src[:8])
	payload := make([]byte, len(src)-8)
	copy(payload, src[8:])
	return Event{Seq: seq, Payload: payload}, len(src), nil
}
