// Package segment 实现只追加事件日志的单段文件：自描述头、逐事件长度前缀
// 与 CRC32 校验。段文件是日志的唯一事实来源（索引可由它完全重建）。
package segment

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"ontology/event"
)

const (
	magic      = "ONSEG" // 5 字节魔数
	version    = byte(1)
	headerLen  = int64(22) // magic5 + ver1 + first8 + count4 + crc4
	dataOff    = headerLen
	lenPrefix  = 4
	seqSize    = 8
	crcSize    = 4
	maxPayload = 64 << 20 // 防御性上限，避免损坏长度前缀导致巨量分配
)

var (
	// ErrHeaderIncomplete：段头不足 22 字节或段头 CRC 不符。
	ErrHeaderIncomplete = errors.New("segment: header incomplete or corrupt")
	// ErrLenIncomplete：记录的 4 字节长度前缀被截断。
	ErrLenIncomplete = errors.New("segment: length prefix incomplete")
	// ErrBodyIncomplete：长度前缀声明的记录体（含 CRC）读不全。
	ErrBodyIncomplete = errors.New("segment: event body incomplete")
	// ErrCRCMismatch：记录完整但 CRC32 不符（原地损坏，截断不会产生）。
	ErrCRCMismatch = errors.New("segment: crc mismatch")
	// ErrBadRecord：长度前缀声明了非法（过大/过小）长度。
	ErrBadRecord = errors.New("segment: illegal record length")
)

// Header 是段头声明：本段首序号与已提交事件数。
type Header struct {
	FirstSeq uint64
	Count    uint32
}

func encodeHeader(dst []byte, h Header) {
	copy(dst[:5], magic)
	dst[5] = version
	binary.BigEndian.PutUint64(dst[6:14], h.FirstSeq)
	binary.BigEndian.PutUint32(dst[14:18], h.Count)
	sum := crc32.ChecksumIEEE(dst[:18])
	binary.BigEndian.PutUint32(dst[18:22], sum)
}

func decodeHeader(raw []byte) (Header, error) {
	if len(raw) < int(headerLen) {
		return Header{}, ErrHeaderIncomplete
	}
	if string(raw[:5]) != magic || raw[5] != version {
		return Header{}, ErrHeaderIncomplete
	}
	want := binary.BigEndian.Uint32(raw[18:22])
	if crc32.ChecksumIEEE(raw[:18]) != want {
		return Header{}, ErrHeaderIncomplete
	}
	return Header{
		FirstSeq: binary.BigEndian.Uint64(raw[6:14]),
		Count:    binary.BigEndian.Uint32(raw[14:18]),
	}, nil
}

// encodeFrame 生成单条记录：len(4)|seq(8)|payload|crc(4)，
// len = 8 + 载荷长 + 4。
func encodeFrame(ev event.Event) []byte {
	body := ev.AppendEncode(nil) // seq+payload
	rec := make([]byte, lenPrefix+len(body)+crcSize)
	binary.BigEndian.PutUint32(rec[:4], uint32(len(body)+crcSize))
	copy(rec[4:], body)
	sum := crc32.ChecksumIEEE(body)
	binary.BigEndian.PutUint32(rec[4+len(body):], sum)
	return rec
}

func framePayloadLen(declared uint32) int {
	if declared < seqSize+crcSize || declared > seqSize+maxPayload+crcSize {
		return -1
	}
	return int(declared) - seqSize - crcSize
}
