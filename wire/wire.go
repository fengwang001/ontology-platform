// Package wire 定义压缩流的字节格式：流头、字面量段、回指、
// 刷新标记与流尾，整数一律使用 uvarint 编码。本包不依赖其他包。
package wire

import "errors"

// 流头：3 字节魔数 + 1 字节版本。
const (
	Magic0  = 'O'
	Magic1  = 'L'
	Magic2  = 'Z'
	Version = 1
)

// 记录标签。
const (
	TagLiteral = 0x00 // uvarint 长度 n(>=1)，随后 n 个原始字节
	TagBackref = 0x01 // uvarint 距离，uvarint 长度
	TagFlush   = 0x02 // 无负载
	TagEnd     = 0x03 // uvarint 原文总长，uvarint 校验和
)

// 可判定的哨兵错误，解压时以 *Error 包装并附带流内字节偏移。
var (
	ErrBadMagic         = errors.New("wire: bad magic")
	ErrBadVersion       = errors.New("wire: unsupported version")
	ErrZeroDistance     = errors.New("wire: zero backref distance")
	ErrDistanceTooFar   = errors.New("wire: distance exceeds bytes output so far")
	ErrDistanceTooLarge = errors.New("wire: distance exceeds window capacity")
	ErrVarintOverflow   = errors.New("wire: varint longer than 10 bytes or overflows 64 bits")
	ErrLengthMismatch   = errors.New("wire: trailer length mismatch")
	ErrChecksumMismatch = errors.New("wire: trailer checksum mismatch")
	ErrTrailingData     = errors.New("wire: data after end of stream")
	ErrTruncated        = errors.New("wire: truncated stream")
	ErrOutputLimit      = errors.New("wire: output limit exceeded")
	ErrUnknownTag       = errors.New("wire: unknown record tag")
)

// Error 携带压缩流中的字节偏移。
type Error struct {
	Offset int64
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Header 返回 4 字节流头。
func Header() []byte { return []byte{Magic0, Magic1, Magic2, Version} }

// AppendUvarint 追加 uvarint 编码（7 位/字节，小端序）。
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// Uvarint 是逐字节喂入的 uvarint 解析器。
type Uvarint struct {
	val   uint64
	shift uint
	n     int
}

func (u *Uvarint) Reset() { u.val, u.shift, u.n = 0, 0, 0 }

// Add 喂入一个字节；done 为真时解析完成。超过 10 字节或溢出 64 位报错。
func (u *Uvarint) Add(b byte) (val uint64, done bool, err error) {
	if u.n == 9 {
		if b > 1 {
			return 0, false, ErrVarintOverflow
		}
		u.n++
		return u.val | uint64(b)<<u.shift, true, nil
	}
	u.val |= uint64(b&0x7f) << u.shift
	u.shift += 7
	u.n++
	if b < 0x80 {
		return u.val, true, nil
	}
	return 0, false, nil
}

// FNV-1a 64 校验和。
const (
	ChecksumOffset = 14695981039346656037
	ChecksumPrime  = 1099511628211
)

func ChecksumAdd(h uint64, b byte) uint64 { return (h ^ uint64(b)) * ChecksumPrime }

// AppendLiteral 追加一个字面量段记录。
func AppendLiteral(dst, p []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

// AppendBackref 追加一个回指记录。
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = append(dst, TagBackref)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}

// AppendFlush 追加刷新标记。
func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

// AppendEnd 追加流尾记录。
func AppendEnd(dst []byte, total, sum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}

// Checksum 计算整段数据的 FNV-1a 64 校验和。
func Checksum(data []byte) uint64 {
	h := uint64(ChecksumOffset)
	for _, b := range data {
		h = ChecksumAdd(h, b)
	}
	return h
}
