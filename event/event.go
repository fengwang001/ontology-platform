// Package event 定义只追加日志中的事件及其编解码。
package event

import "encoding/binary"

// Event 是一条带全局序号的事件。序号在一个日志内连续、0 基。
type Event struct {
	Seq  uint64
	Data []byte
}

// EncodeBody 将事件编码为记录体（不含长度前缀与 CRC）：
// 序号 u64 大端 + 原始载荷。空载荷合法。
func EncodeBody(e Event) []byte {
	buf := make([]byte, 8+len(e.Data))
	binary.BigEndian.PutUint64(buf, e.Seq)
	copy(buf[8:], e.Data)
	return buf
}

// DecodeBody 解析 EncodeBody 产生的记录体。
func DecodeBody(buf []byte) (Event, error) {
	if len(buf) < 8 {
		return Event{}, ErrShortBody
	}
	seq := binary.BigEndian.Uint64(buf)
	data := make([]byte, len(buf)-8)
	copy(data, buf[8:])
	return Event{Seq: seq, Data: data}, nil
}

// EncodedLen 返回该事件记录体的字节长度。
func (e Event) EncodedLen() int { return 8 + len(e.Data) }
