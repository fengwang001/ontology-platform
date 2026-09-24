// Package event 定义只追加日志中的事件及其二进制编解码。
//
// 记录线格式（大端）：
//
//	len(4) | seq(8) | payload(len-8) | crc32-IEEE(4)
//
// len 为 seq+payload 的字节数；CRC 覆盖 seq+payload。空载荷合法。
package event

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Event 是一条带全局连续序号的事件。
type Event struct {
	Seq     uint64
	Payload []byte
}

const (
	lenSize = 4
	seqSize = 8
	crcSize = 4

	// MinRecordLen 是一条空载荷记录的长度：4+8+4。
	MinRecordLen = lenSize + seqSize + crcSize
	// MaxPayloadLen 是单条载荷上限（受 32 位长度前缀约束）。
	MaxPayloadLen = 1<<32 - 1 - seqSize
)

var (
	// ErrShortRecord 记录字节数连最小定长部分都不够。
	ErrShortRecord = errors.New("event: record too short")
	// ErrLenMismatch 声明长度与实际记录长度不符。
	ErrLenMismatch = errors.New("event: declared length mismatch")
	// ErrCRCMismatch CRC 校验失败。
	ErrCRCMismatch = errors.New("event: crc mismatch")
	// ErrPayloadTooLarge 载荷超过长度前缀可表达范围。
	ErrPayloadTooLarge = errors.New("event: payload too large")
)

var crcTable = crc32.MakeTable(crc32.IEEE)

// RecordLen 返回事件编码后的完整记录长度。
func RecordLen(payloadLen int) int {
	return lenSize + seqSize + payloadLen + crcSize
}

// Encode 将事件编码为一条自描述记录。
func (e Event) Encode() ([]byte, error) {
	if len(e.Payload) > MaxPayloadLen {
		return nil, ErrPayloadTooLarge
	}
	body := make([]byte, seqSize+len(e.Payload))
	binary.BigEndian.PutUint64(body, e.Seq)
	copy(body[seqSize:], e.Payload)

	rec := make([]byte, lenSize+len(body)+crcSize)
	binary.BigEndian.PutUint32(rec, uint32(len(body)))
	copy(rec[lenSize:], body)
	sum := crc32.Checksum(body, crcTable)
	binary.BigEndian.PutUint32(rec[lenSize+len(body):], sum)
	return rec, nil
}

// DecodeRecord 解析一条完整记录（含长度前缀与 CRC）。
func DecodeRecord(rec []byte) (Event, error) {
	if len(rec) < MinRecordLen {
		return Event{}, ErrShortRecord
	}
	bodyLen := int(binary.BigEndian.Uint32(rec))
	if bodyLen < seqSize || len(rec) != lenSize+bodyLen+crcSize {
		return Event{}, ErrLenMismatch
	}
	body := rec[lenSize : lenSize+bodyLen]
	want := binary.BigEndian.Uint32(rec[lenSize+bodyLen:])
	if crc32.Checksum(body, crcTable) != want {
		return Event{}, ErrCRCMismatch
	}
	e := Event{
		Seq:     binary.BigEndian.Uint64(body),
		Payload: append([]byte(nil), body[seqSize:]...),
	}
	return e, nil
}

// DecodeBody 解析「长度前缀之后、CRC 之前」的记录体并校验外部 CRC。
// 供顺序读使用：调用方已按长度前缀切分出 body 与 crc。
func DecodeBody(body, crc []byte) (Event, error) {
	if len(body) < seqSize || len(crc) != crcSize {
		return Event{}, ErrLenMismatch
	}
	if crc32.Checksum(body, crcTable) != binary.BigEndian.Uint32(crc) {
		return Event{}, ErrCRCMismatch
	}
	return Event{
		Seq:     binary.BigEndian.Uint64(body),
		Payload: append([]byte(nil), body[seqSize:]...),
	}, nil
}
