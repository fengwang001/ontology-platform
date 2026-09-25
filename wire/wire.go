// Package wire 定义压缩流的字节格式：流头、字面量段、回指、
// 刷新标记与流尾，全部整数用 uvarint（LEB128）编码。
package wire

import (
	"errors"
	"strconv"
)

// 流头：魔数 + 版本，各 1 字节定长。
const (
	Magic   byte = 0x9A
	Version byte = 0x01
)

// 记录类型，存放在 tag 低 2 位。
const (
	TagLiteral = 0 // tag=(len<<2)|0，随后 len 个原始字节
	TagBackref = 1 // tag=(len<<2)|1，随后 uvarint dist
	TagFlush   = 2 // 单字节，无负载
	TagEnd     = 3 // 随后 uvarint 原文总长、uvarint 校验和
)

// 解压/校验的哨兵错误，一律以 *Error 包装并携带字节偏移。
var (
	ErrMagic       = errors.New("wire: bad magic")
	ErrVersion     = errors.New("wire: bad version")
	ErrDistZero    = errors.New("wire: backref distance is zero")
	ErrDistOutput  = errors.New("wire: backref distance beyond output")
	ErrDistWindow  = errors.New("wire: backref distance beyond window capacity")
	ErrVarint      = errors.New("wire: varint overflow")
	ErrLenMismatch = errors.New("wire: trailing length mismatch")
	ErrChecksum    = errors.New("wire: trailing checksum mismatch")
	ErrTrailing    = errors.New("wire: trailing data after end")
	ErrTruncated   = errors.New("wire: truncated stream")
	ErrOutputLimit = errors.New("wire: output limit exceeded")
)

// Error 携带出错记录在压缩流中的字节偏移。
type Error struct {
	Kind error
	Off  int64
}

func (e *Error) Error() string { return e.Kind.Error() + " at offset " + strconv.FormatInt(e.Off, 10) }
func (e *Error) Unwrap() error { return e.Kind }

// AppendUvarint 以 LEB128 追加编码 v。
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// Header 返回流头字节。
func Header() []byte { return []byte{Magic, Version} }

// AppendLiteral 追加一个字面量段记录。
func AppendLiteral(dst, payload []byte) []byte {
	dst = AppendUvarint(dst, uint64(len(payload))<<2|TagLiteral)
	return append(dst, payload...)
}

// AppendBackref 追加一个回指记录。
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = AppendUvarint(dst, uint64(length)<<2|TagBackref)
	return AppendUvarint(dst, uint64(dist))
}

// AppendFlush 追加刷新标记。
func AppendFlush(dst []byte) []byte { return AppendUvarint(dst, TagFlush) }

// AppendEnd 追加流尾：原文总长与校验和。
func AppendEnd(dst []byte, total, sum uint64) []byte {
	dst = AppendUvarint(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}

// 校验和：FNV-1a 64，逐字节滚动。
const SumSeed uint64 = 14695981039346656037

// SumByte 把一个字节滚入校验和。
func SumByte(h uint64, b byte) uint64 { return (h ^ uint64(b)) * 1099511628211 }

// SumOf 返回整段数据的校验和。
func SumOf(data []byte) uint64 {
	h := SumSeed
	for _, b := range data {
		h = SumByte(h, b)
	}
	return h
}
