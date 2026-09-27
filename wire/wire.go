// Package wire 定义自定义 LZ77 压缩流的字节格式。不依赖其他包。
package wire

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
)

const (
	Magic0, Magic1, Magic2, Magic3 = 0x4C, 0x5A, 0x4C, 0x5A
	Version                        = 1

	TagLiteral = 0 // payload=长度，后接原文
	TagMatch   = 1 // payload=距离，后接长度
	TagFlush   = 2 // 刷新标记
	TagEnd     = 3 // payload 位保留为 0，后接原文总长、校验和
)

var (
	ErrOverflow = errors.New("wire: varint overflow (>10 bytes or >64-bit)")
	ErrTruncated = errors.New("wire: truncated stream")
)

// Header 返回流头字节。
func Header() []byte { return []byte{Magic0, Magic1, Magic2, Magic3, Version} }

func putUvarint(dst []byte, v uint64) []byte {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], v)
	return append(dst, b[:n]...)
}

// Literal 编码一个字面量段。
func Literal(dst, data []byte) []byte {
	dst = putUvarint(dst, uint64(TagLiteral)|uint64(len(data))<<2)
	return append(dst, data...)
}

// Match 编码一条回指。
func Match(dst []byte, dist, length int) []byte {
	dst = putUvarint(dst, uint64(TagMatch)|uint64(dist)<<2)
	return putUvarint(dst, uint64(length))
}

// Flush 编码刷新标记。
func Flush(dst []byte) []byte { return putUvarint(dst, TagFlush) }

// End 编码流尾。
func End(dst []byte, totalLen, checksum uint64) []byte {
	dst = putUvarint(dst, TagEnd)
	dst = putUvarint(dst, totalLen)
	return putUvarint(dst, checksum)
}

// ReadUvarint 从 data[start:] 读取 LEB128。
// 返回值、新偏移；不足返回 ErrTruncated，超长/溢出返回 ErrOverflow。
func ReadUvarint(data []byte, start int) (uint64, int, error) {
	var x uint64
	var s uint
	for i := start; i < len(data); i++ {
		b := data[i]
		if i-start == binary.MaxVarintLen64-1 {
			if b > 1 {
				return 0, i + 1, ErrOverflow
			}
		}
		if i-start >= binary.MaxVarintLen64 {
			return 0, i, ErrOverflow
		}
		if b < 0x80 {
			return x | uint64(b)<<s, i + 1, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, start, ErrTruncated
}

// Checksum 返回 FNV-1a 32 位校验和。
func Checksum(data []byte) uint64 {
	h := fnv.New32a()
	h.Write(data)
	return uint64(h.Sum32())
}
