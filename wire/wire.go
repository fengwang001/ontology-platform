// Package wire 定义自定 LZ77 压缩流的字节格式。
package wire

import (
	"errors"
	"hash/crc32"
	"io"
)

const (
	Magic   = "LZ77ONTO"
	Version = uint64(1)

	TagLiteral = 0
	TagRef     = 1
	TagFlush   = 2
	TagEnd     = 3
)

var (
	ErrVarintOverflow = errors.New("wire: varint exceeds 64 bits")
	ErrHeaderMagic    = errors.New("wire: bad header magic")
	ErrHeaderVersion  = errors.New("wire: unsupported stream version")
)

// AppendVarint 追加 unsigned LEB128 编码。
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadVarint 从 p 头部解码一个 varint。
// 返回 io.ErrUnexpectedEOF 表示数据尚未到齐，ErrVarintOverflow 表示超长/溢出。
func ReadVarint(p []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if i >= len(p) {
			return 0, i, io.ErrUnexpectedEOF
		}
		b := p[i]
		if i == 9 && b > 1 {
			return 0, i + 1, ErrVarintOverflow
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 10, ErrVarintOverflow
}

func AppendHeader(dst []byte, windowSize int) []byte {
	dst = append(dst, Magic...)
	dst = AppendVarint(dst, Version)
	dst = AppendVarint(dst, uint64(windowSize))
	return dst
}

// ParseHeader 解析流头，返回消费的字节数与窗口容量。
func ParseHeader(p []byte) (int, int, error) {
	if len(p) < len(Magic) {
		return 0, 0, io.ErrUnexpectedEOF
	}
	if string(p[:len(Magic)]) != Magic {
		return 0, 0, ErrHeaderMagic
	}
	off := len(Magic)
	ver, n, err := ReadVarint(p[off:])
	if err != nil {
		return 0, 0, err
	}
	off += n
	if ver != Version {
		return 0, 0, ErrHeaderVersion
	}
	win, n, err := ReadVarint(p[off:])
	if err != nil {
		return 0, 0, err
	}
	off += n
	if win > uint64(^uint(0)>>1) {
		return 0, 0, ErrHeaderMagic
	}
	return off, int(win), nil
}

func AppendLiteral(dst, data []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendVarint(dst, uint64(len(data)))
	return append(dst, data...)
}

func AppendRef(dst []byte, dist, length int) []byte {
	dst = append(dst, TagRef)
	dst = AppendVarint(dst, uint64(dist))
	dst = AppendVarint(dst, uint64(length))
	return dst
}

func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

func AppendEnd(dst []byte, total uint64, sum uint32) []byte {
	dst = append(dst, TagEnd)
	dst = AppendVarint(dst, total)
	return append(dst,
		byte(sum), byte(sum>>8), byte(sum>>16), byte(sum>>24))
}

var IEEETable = crc32.IEEETable
