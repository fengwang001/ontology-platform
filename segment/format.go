// Package segment 定义有序段文件格式并提供读写。
//
// 文件布局：[header][entry]*[index entry]*[footerCRC]
// header 36B，自描述；entry 为有序键值条目，valLen==-1 表示删除标记；
// 稀疏索引每 stride 条取样一个键；footerCRC 覆盖 header 之后的全部字节。
package segment

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	Version    = 1
	Stride     = 16 // 稀疏索引取样间隔
	HeaderSize = 36
	tombstone  = -1
)

var magic = [4]byte{'O', 'K', 'V', '1'}

// 截断/损坏分类错误，均可用 errors.Is 判定。
var (
	ErrHeaderIncomplete = errors.New("segment: header incomplete")
	ErrEntryTruncated   = errors.New("segment: entry region truncated")
	ErrIndexIncomplete  = errors.New("segment: index region incomplete")
	ErrCRCMismatch      = errors.New("segment: crc mismatch")
	ErrUnsorted         = errors.New("segment: keys must be added in sorted order")
)

// State 是点查结果的三态：有值 / 已删除 / 从未写过。
type State int

const (
	StateAbsent  State = iota // 从未写过
	StateValue                // 有值（含空字节串值）
	StateDeleted              // 已删除（删除标记）
)

type header struct {
	entries     uint32
	indexOffset uint64
	indexLen    uint64
	indexCount  uint32
}

func encodeHeader(h header) []byte {
	b := make([]byte, HeaderSize)
	copy(b[0:4], magic[:])
	binary.LittleEndian.PutUint16(b[4:6], Version)
	binary.LittleEndian.PutUint16(b[6:8], Stride)
	binary.LittleEndian.PutUint32(b[8:12], h.entries)
	binary.LittleEndian.PutUint64(b[12:20], h.indexOffset)
	binary.LittleEndian.PutUint64(b[20:28], h.indexLen)
	binary.LittleEndian.PutUint32(b[28:32], h.indexCount)
	binary.LittleEndian.PutUint32(b[32:36], crc32.ChecksumIEEE(b[0:32]))
	return b
}

func decodeHeader(b []byte) (header, error) {
	var h header
	if len(b) < HeaderSize {
		return h, ErrHeaderIncomplete
	}
	if string(b[0:4]) != string(magic[:]) ||
		binary.LittleEndian.Uint16(b[4:6]) != Version ||
		crc32.ChecksumIEEE(b[0:32]) != binary.LittleEndian.Uint32(b[32:36]) {
		return h, ErrCRCMismatch
	}
	h.entries = binary.LittleEndian.Uint32(b[8:12])
	h.indexOffset = binary.LittleEndian.Uint64(b[12:20])
	h.indexLen = binary.LittleEndian.Uint64(b[20:28])
	h.indexCount = binary.LittleEndian.Uint32(b[28:32])
	return h, nil
}

// encodeEntry 追加编码一条条目：u32keyLen i32valLen key val u32crc。
func encodeEntry(dst, key, val []byte, deleted bool) []byte {
	vl := int32(len(val))
	if deleted {
		vl = tombstone
	}
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(key)))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(vl))
	start := len(dst)
	dst = append(dst, hdr[:]...)
	dst = append(dst, key...)
	if !deleted {
		dst = append(dst, val...)
	}
	crc := crc32.ChecksumIEEE(dst[start:])
	return binary.LittleEndian.AppendUint32(dst, crc)
}

type indexEntry struct {
	key string
	off uint64
}

func encodeIndexEntry(dst []byte, e indexEntry) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(e.key)))
	dst = append(dst, e.key...)
	return binary.LittleEndian.AppendUint64(dst, e.off)
}
