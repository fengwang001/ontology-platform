// Package wire 定义压缩流的字节格式：流头、字面量段、回指、刷新标记与流尾。
// 所有记录用 uvarint 编码，tag 低 2 位为记录类型。本包不依赖其他包。
package wire

import (
	"encoding/binary"
	"hash/fnv"
)

// 流头：2 字节魔数 + 1 字节版本。
const (
	Magic0  byte = 0x4C
	Magic1  byte = 0x5A
	Version byte = 0x01
)

// 记录类型（tag 低 2 位）。
const (
	TagLiteral = 0 // 字面量段：tag=(len<<2)|0，随后 len 个原始字节
	TagBackref = 1 // 回指：tag=(len<<2)|1，随后 uvarint dist
	TagFlush   = 2 // 刷新标记：tag 值恰为 2
	TagTrailer = 3 // 流尾：tag 值恰为 3，随后总长与校验和两个 uvarint
)

// Header 返回流头字节。
func Header() []byte { return []byte{Magic0, Magic1, Version} }

// AppendLiteral 追加一个字面量段记录。
func AppendLiteral(dst, p []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(p))<<2|TagLiteral)
	return append(dst, p...)
}

// AppendBackref 追加一个回指记录（dist>=1，length>=1）。
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = binary.AppendUvarint(dst, uint64(length)<<2|TagBackref)
	return binary.AppendUvarint(dst, uint64(dist))
}

// AppendFlush 追加刷新标记。
func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

// AppendTrailer 追加流尾：原文总长与校验和。
func AppendTrailer(dst []byte, total, sum uint64) []byte {
	dst = append(dst, TagTrailer)
	dst = binary.AppendUvarint(dst, total)
	return binary.AppendUvarint(dst, sum)
}

// Checksum 计算原文的 FNV-1a 64 位校验和。
func Checksum(b []byte) uint64 {
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}
