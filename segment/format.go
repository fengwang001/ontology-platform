// Package segment 实现只追加段文件：自描述头 + 逐事件长度前缀记录 + CRC32。
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"ontology/event"
)

// 段头布局：magic(4) + version(2) + firstSeq(8) + count(8) + headerCRC32(4)。
const (
	Magic      = "OSEG"
	Version    = 1
	HeaderSize = 26
	headerBody = HeaderSize - 4 // CRC 覆盖的字节数
)

// 损坏分类错误，均可用 errors.Is 判定。
var (
	ErrHeaderIncomplete       = errors.New("segment: header incomplete")
	ErrLengthPrefixIncomplete = errors.New("segment: length prefix incomplete")
	ErrBodyIncomplete         = errors.New("segment: event body incomplete")
	ErrCRCMismatch            = errors.New("segment: crc mismatch")
	ErrBadHeader              = errors.New("segment: bad magic or version")
)

// Header 是段文件的自描述头。
type Header struct {
	FirstSeq uint64
	Count    uint64
}

// LastSeq 返回段内最后一条事件的序号；段为空时返回 FirstSeq。
func (h Header) LastSeq() uint64 {
	if h.Count == 0 {
		return h.FirstSeq
	}
	return h.FirstSeq + h.Count - 1
}

// EncodeHeader 序列化段头（含 CRC）。
func EncodeHeader(h Header) []byte {
	buf := make([]byte, HeaderSize)
	copy(buf, Magic)
	binary.BigEndian.PutUint16(buf[4:], Version)
	binary.BigEndian.PutUint64(buf[6:], h.FirstSeq)
	binary.BigEndian.PutUint64(buf[14:], h.Count)
	binary.BigEndian.PutUint32(buf[22:], crc32.ChecksumIEEE(buf[:headerBody]))
	return buf
}

// DecodeHeader 解析段头；字节不足报 ErrHeaderIncomplete，CRC 不符报 ErrCRCMismatch。
func DecodeHeader(buf []byte) (Header, error) {
	if len(buf) < HeaderSize {
		return Header{}, fmt.Errorf("%w: have %d of %d bytes", ErrHeaderIncomplete, len(buf), HeaderSize)
	}
	if string(buf[:4]) != Magic || binary.BigEndian.Uint16(buf[4:]) != Version {
		return Header{}, ErrBadHeader
	}
	if crc32.ChecksumIEEE(buf[:headerBody]) != binary.BigEndian.Uint32(buf[22:]) {
		return Header{}, fmt.Errorf("%w: header", ErrCRCMismatch)
	}
	return Header{FirstSeq: binary.BigEndian.Uint64(buf[6:]), Count: binary.BigEndian.Uint64(buf[14:])}, nil
}

// RecordSize 返回一条记录（长度前缀 + 事件体 + CRC）的总字节数。
func RecordSize(e event.Event) int { return 4 + e.Size() + 4 }

// EncodeRecord 编码一条记录：len(4) + 事件体 + CRC32(4，覆盖 len+事件体)。
func EncodeRecord(e event.Event) []byte {
	body := event.Encode(e)
	buf := make([]byte, 4+len(body)+4)
	binary.BigEndian.PutUint32(buf, uint32(len(body)))
	copy(buf[4:], body)
	binary.BigEndian.PutUint32(buf[4+len(body):], crc32.ChecksumIEEE(buf[:4+len(body)]))
	return buf
}

// DecodeRecord 从 buf 起始处解码一条记录，返回事件与记录总字节数。
// 截断位置决定返回的错误类别，四类均可用 errors.Is 区分。
func DecodeRecord(buf []byte) (event.Event, int, error) {
	if len(buf) < 4 {
		return event.Event{}, 0, fmt.Errorf("%w: have %d of 4 bytes", ErrLengthPrefixIncomplete, len(buf))
	}
	bodyLen := int(binary.BigEndian.Uint32(buf))
	if len(buf) < 4+bodyLen {
		return event.Event{}, 0, fmt.Errorf("%w: have %d of %d bytes", ErrBodyIncomplete, len(buf)-4, bodyLen)
	}
	if len(buf) < 4+bodyLen+4 {
		return event.Event{}, 0, fmt.Errorf("%w: crc field truncated", ErrCRCMismatch)
	}
	if crc32.ChecksumIEEE(buf[:4+bodyLen]) != binary.BigEndian.Uint32(buf[4+bodyLen:]) {
		return event.Event{}, 0, fmt.Errorf("%w: record", ErrCRCMismatch)
	}
	ev, err := event.Decode(buf[4 : 4+bodyLen])
	if err != nil {
		return event.Event{}, 0, fmt.Errorf("%w: %v", ErrBodyIncomplete, err)
	}
	return ev, 4 + bodyLen + 4, nil
}
