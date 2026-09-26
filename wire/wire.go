// Package wire 定义压缩流的字节格式：流头、字面量段、回指、刷新标记与流尾。
// 所有整数均为 uvarint（LEB128）编码。本包不依赖其他包。
package wire

import (
	"encoding/binary"
	"io"
)

// 流头常量。
const (
	Magic   uint64 = 0x4C5A // "LZ"
	Version uint64 = 1
)

// 记录标签。
const (
	TagLiteral uint64 = iota // 字面量段：tag, n, n 字节原文
	TagBackref               // 回指：tag, dist, len
	TagFlush                 // 刷新标记：tag
	TagTrailer               // 流尾：tag, totalLen, checksum
)

// Writer 把记录写入底层 io.Writer，首个错误被记住，后续调用为空操作。
type Writer struct {
	W   io.Writer
	Err error
	lit []byte // 未发出的字面量缓冲
}

// Uvarint 写入一个变长整数。
func (s *Writer) Uvarint(v uint64) {
	if s.Err != nil {
		return
	}
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	_, s.Err = s.W.Write(buf[:n])
}

func (s *Writer) bytes(p []byte) {
	if s.Err != nil {
		return
	}
	_, s.Err = s.W.Write(p)
}

// Header 写流头。
func (s *Writer) Header() { s.Uvarint(Magic); s.Uvarint(Version) }

// Literal 立即写字面量段。
func (s *Writer) Literal(p []byte) {
	s.Uvarint(TagLiteral)
	s.Uvarint(uint64(len(p)))
	s.bytes(p)
}

// Lit 把一个字节攒进字面量缓冲，由 Match/EndLiterals/Flush 统一发出。
func (s *Writer) Lit(b byte) { s.lit = append(s.lit, b) }

// EndLiterals 发出积攒的字面量段（若有）。
func (s *Writer) EndLiterals() {
	if len(s.lit) > 0 {
		s.Literal(s.lit)
		s.lit = s.lit[:0]
	}
}

// Match 先发出积攒的字面量段，再写回指。
func (s *Writer) Match(dist, length int) {
	s.EndLiterals()
	s.Uvarint(TagBackref)
	s.Uvarint(uint64(dist))
	s.Uvarint(uint64(length))
}

// Flush 发出积攒的字面量段并写刷新标记。
func (s *Writer) Flush() { s.EndLiterals(); s.Uvarint(TagFlush) }

// Trailer 写流尾：原文总长与 64 位校验和。
func (s *Writer) Trailer(total int64, sum uint64) {
	s.EndLiterals()
	s.Uvarint(TagTrailer)
	s.Uvarint(uint64(total))
	s.Uvarint(sum)
}
