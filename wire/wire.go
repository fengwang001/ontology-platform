// Package wire 定义压缩流的字节格式：流头、字面量段、回指、
// 刷新标记与流尾，整数一律使用 uvarint 编码。不依赖其他包。
package wire

import "encoding/binary"

// 流头：3 字节魔数 + 1 字节版本。
const (
	Magic0  = 'L'
	Magic1  = 'Z'
	Magic2  = '7'
	Version = 0x01
)

// 记录标签。
const (
	TagLiteral = 0 // + 长度 n + n 字节原文
	TagRef     = 1 // + 距离 + 长度
	TagFlush   = 2 // 无负载
	TagEnd     = 3 // + 原文总长 + 校验和
)

// FNV-1a-64，用于流尾校验和。
const (
	SumInit  uint64 = 14695981039346656037
	sumPrime        = 1099511628211
)

// SumByte 把一个字节混入校验和。
func SumByte(s uint64, b byte) uint64 { return (s ^ uint64(b)) * sumPrime }

// SumBytes 计算整段数据的校验和。
func SumBytes(data []byte) uint64 {
	s := SumInit
	for _, b := range data {
		s = SumByte(s, b)
	}
	return s
}

// AppendUvarint 追加一个 uvarint。
func AppendUvarint(dst []byte, v uint64) []byte { return binary.AppendUvarint(dst, v) }

// AppendHeader 追加流头。
func AppendHeader(dst []byte) []byte {
	return append(dst, Magic0, Magic1, Magic2, Version)
}

// AppendLiteral 追加一个字面量段。
func AppendLiteral(dst, lit []byte) []byte {
	dst = AppendUvarint(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(lit)))
	return append(dst, lit...)
}

// AppendRef 追加一个回指（允许 dist < n 的重叠回指）。
func AppendRef(dst []byte, dist, n int) []byte {
	dst = AppendUvarint(dst, TagRef)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(n))
}

// AppendFlush 追加刷新标记。
func AppendFlush(dst []byte) []byte { return AppendUvarint(dst, TagFlush) }

// AppendEnd 追加流尾：原文总长 + 校验和。
func AppendEnd(dst []byte, total, sum uint64) []byte {
	dst = AppendUvarint(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}
