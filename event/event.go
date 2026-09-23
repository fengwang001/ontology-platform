// Package event 定义事件（序号 + 载荷）与单条事件记录的编解码。
//
// 段内记录布局（小端）：len u32 | payload | crc32 IEEE u32；
// CRC 覆盖 len||payload。序号不进记录，由段头 firstSeq + 段内下标推出。
package event

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Event 是一条带序号的事件。
type Event struct {
	Seq     uint64
	Payload []byte
}

const (
	// LenSize 是长度前缀字节数。
	LenSize = 4
	// CRCSize 是尾部 CRC 字节数。
	CRCSize = 4
)

// 截断点分类用哨兵错误，调用方用 errors.Is 区分。
var (
	// ErrLengthIncomplete：剩余不足 4 字节，长度前缀不完整。
	ErrLengthIncomplete = errors.New("event: length prefix incomplete")
	// ErrBodyIncomplete：长度已知但 payload 不足。
	ErrBodyIncomplete = errors.New("event: body incomplete")
	// ErrCRCMismatch：CRC 字段缺失（截断）或校验不符（篡改）。
	ErrCRCMismatch = errors.New("event: crc mismatch")
)

// EncodedSize 返回给定载荷长度的完整记录字节数。
func EncodedSize(payloadLen int) int {
	return LenSize + payloadLen + CRCSize
}

// Encode 把载荷编码为一条完整记录（允许空载荷）。
func Encode(payload []byte) []byte {
	rec := make([]byte, EncodedSize(len(payload)))
	binary.LittleEndian.PutUint32(rec, uint32(len(payload)))
	copy(rec[LenSize:], payload)
	crc := crc32.ChecksumIEEE(rec[:LenSize+len(payload)])
	binary.LittleEndian.PutUint32(rec[LenSize+len(payload):], crc)
	return rec
}

// Decode 从 buf 头部解码一条记录，返回载荷副本与总占用字节数。
//
// buf 不足长度前缀 → ErrLengthIncomplete；不足 payload → ErrBodyIncomplete；
// CRC 字段不完整或校验不符 → ErrCRCMismatch。
func Decode(buf []byte) ([]byte, int, error) {
	if len(buf) < LenSize {
		return nil, 0, fmt.Errorf("%w: %d byte(s) remain", ErrLengthIncomplete, len(buf))
	}
	payloadLen := int(binary.LittleEndian.Uint32(buf))
	if len(buf) < LenSize+payloadLen {
		return nil, 0, fmt.Errorf("%w: need %d payload byte(s), have %d",
			ErrBodyIncomplete, payloadLen, len(buf)-LenSize)
	}
	if len(buf) < LenSize+payloadLen+CRCSize {
		return nil, 0, fmt.Errorf("%w: %d of %d crc byte(s) present",
			ErrCRCMismatch, len(buf)-(LenSize+payloadLen), CRCSize)
	}
	want := crc32.ChecksumIEEE(buf[:LenSize+payloadLen])
	if got := binary.LittleEndian.Uint32(buf[LenSize+payloadLen:]); got != want {
		return nil, 0, fmt.Errorf("%w: got %08x want %08x", ErrCRCMismatch, got, want)
	}
	payload := make([]byte, payloadLen)
	copy(payload, buf[LenSize:LenSize+payloadLen])
	return payload, LenSize + payloadLen + CRCSize, nil
}
