// Package event 定义只追加日志中的事件及其载荷编解码。
//
// 事件的"线路编码"为 8 字节大端序号后接原始载荷；帧定界（长度前缀、CRC）
// 由 segment 层负责，event 层只关心 seq+payload 这一逻辑内容。
package event

import (
	"encoding/binary"
	"errors"
)

// Event 是日志中的一条事件：单调递增的序号与不透明字节载荷。
type Event struct {
	Seq     uint64
	Payload []byte
}

// EncodedLen 返回 Encode 产生的字节数（8 + 载荷长度）。
func (e Event) EncodedLen() int { return 8 + len(e.Payload) }

// Encode 把事件写入 dst；容量不足时返回 ErrShortBuffer。
func (e Event) Encode(dst []byte) error {
	if len(dst) < e.EncodedLen() {
		return ErrShortBuffer
	}
	binary.BigEndian.PutUint64(dst[:8], e.Seq)
	copy(dst[8:], e.Payload)
	return nil
}

// Decode 从 seq||payload 形式的字节还原事件；不足 8 字节报错。
// 返回的事件持有 src 载荷切片的副本，调用方随后改写 src 不影响事件。
func Decode(src []byte) (Event, error) {
	if len(src) < 8 {
		return Event{}, ErrShortBuffer
	}
	payload := make([]byte, len(src)-8)
	copy(payload, src[8:])
	return Event{Seq: binary.BigEndian.Uint64(src[:8]), Payload: payload}, nil
}

// AppendEncode 返回 append(seq, payload...) 形式的新切片，
// 便于直接喂给 segment 的 CRC 计算。
func (e Event) AppendEncode(dst []byte) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, e.Seq)
	dst = append(dst, buf...)
	return append(dst, e.Payload...)
}

// ErrShortBuffer 表示目标缓冲区装不下编码，或输入短于 8 字节。
var ErrShortBuffer = errors.New("event: short buffer")
