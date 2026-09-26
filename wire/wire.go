// Package wire 定义压缩流的字节格式：流头、字面量段、回指、刷新标记与流尾。
// 所有整数均为 uvarint（LEB128），本包不依赖工程内其他包。
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// 记录类型标签。
const (
	TagLiteral = 0x01 // + uvarint 长度 n + n 字节原文
	TagMatch   = 0x02 // + uvarint 距离 + uvarint 长度
	TagFlush   = 0x03 // 无负载
	TagTail    = 0x04 // + uvarint 原文总长 + uvarint CRC32
)

const (
	Version   = 1
	headLen   = 4 // 3 字节魔数 + 1 字节版本
	maxVarint = 10
)

var magic = []byte{'O', 'L', 'Z'}

// 可判定的哨兵错误，用 errors.Is 判断。
var (
	ErrBadMagic   = errors.New("wire: 流头魔数错误")
	ErrBadVersion = errors.New("wire: 流头版本错误")
	ErrVarint     = errors.New("wire: 变长整数超长或溢出")
	ErrUnknownTag = errors.New("wire: 未知记录标签")
	ErrTruncated  = errors.New("wire: 流被截断")
)

// Error 携带压缩流中的字节偏移。
type Error struct {
	Offset int64
	Err    error
}

func (e *Error) Error() string { return fmt.Sprintf("偏移 %d: %v", e.Offset, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// Header 返回流头字节。
func Header() []byte { return []byte{'O', 'L', 'Z', Version} }

// CheckHeader 校验流头，返回消耗的字节数。
func CheckHeader(src []byte) (int, error) {
	if len(src) < headLen {
		return 0, ErrTruncated
	}
	for i := range magic {
		if src[i] != magic[i] {
			return 0, &Error{Offset: int64(i), Err: ErrBadMagic}
		}
	}
	if src[3] != Version {
		return 0, &Error{Offset: 3, Err: ErrBadVersion}
	}
	return headLen, nil
}

// AppendUvarint 追加一个变长整数。
func AppendUvarint(dst []byte, v uint64) []byte {
	var buf [maxVarint]byte
	n := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:n]...)
}

// ReadUvarint 从 src 读一个变长整数，返回值、消耗字节数、错误。
// 超过 10 字节或溢出 64 位返回 ErrVarint；字节不足返回 ErrTruncated。
func ReadUvarint(src []byte, off int64) (uint64, int, error) {
	v, n := binary.Uvarint(src)
	if n > 0 {
		return v, n, nil
	}
	if n == 0 {
		return 0, 0, &Error{Offset: off, Err: ErrTruncated}
	}
	return 0, 0, &Error{Offset: off, Err: ErrVarint}
}

// AppendLiteral 追加一个字面量段记录。
func AppendLiteral(dst, lit []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(lit)))
	return append(dst, lit...)
}

// AppendMatch 追加一个回指记录。
func AppendMatch(dst []byte, dist, length int) []byte {
	dst = append(dst, TagMatch)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}

// AppendFlush 追加一个刷新标记。
func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

// AppendTail 追加流尾（原文总长 + CRC32 校验和）。
func AppendTail(dst []byte, total, sum uint64) []byte {
	dst = append(dst, TagTail)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}

// Checksum 计算原文校验和。
func Checksum(src []byte) uint64 { return uint64(crc32.ChecksumIEEE(src)) }
