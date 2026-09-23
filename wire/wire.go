// Package wire 定义自定义 LZ77 压缩流的字节格式。
package wire

import "errors"

// 记录标记字节。
const (
	TagHeader  byte = 'H'
	TagLiteral byte = 'L'
	TagRef     byte = 'R'
	TagFlush   byte = 'F'
	TagTail    byte = 'T'
)

const Version = 1

// 可判定的哨兵错误；解码错误由 FormatError 包裹并附带字节偏移。
var (
	ErrTruncated   = errors.New("wire: compressed stream truncated")
	ErrBadMagic    = errors.New("wire: bad header magic")
	ErrBadVersion  = errors.New("wire: unsupported version")
	ErrVarint      = errors.New("wire: varint too long or overflows 64 bits")
	ErrBadRecord   = errors.New("wire: unknown record tag")
	ErrBadLength   = errors.New("wire: invalid length in record")
	ErrDistZero    = errors.New("wire: back-reference distance is zero")
	ErrDistOutput  = errors.New("wire: distance exceeds bytes produced")
	ErrDistWindow  = errors.New("wire: distance exceeds window capacity")
	ErrSizeMismatch = errors.New("wire: tail total length mismatch")
	ErrChecksum    = errors.New("wire: checksum mismatch")
	ErrTrailing    = errors.New("wire: trailing bytes after tail")
	ErrOutputLimit = errors.New("wire: output exceeds configured limit")
)

// FormatError 把哨兵错误与压缩流中的字节偏移绑定。
type FormatError struct {
	Offset int
	Err    error
}

func (e *FormatError) Error() string { return e.Err.Error() }
func (e *FormatError) Unwrap() error { return e.Err }

// AppendUvarint 追加 LEB128 无符号变长整数。
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint 从 b[off:] 读一个 uvarint。err 为 ErrTruncated（数据中断）
// 或 ErrVarint（超过 10 字节 / 64 位溢出）。
func ReadUvarint(b []byte, off int) (v uint64, newOff int, err error) {
	var x uint64
	for i := 0; ; i++ {
		if off >= len(b) {
			return 0, off, ErrTruncated
		}
		c := b[off]
		off++
		if i == 9 && c > 1 { // 第 10 字节只允许最低 1 位
			return 0, off, ErrVarint
		}
		if i >= 10 {
			return 0, off, ErrVarint
		}
		x |= uint64(c&0x7f) << (uint(i) * 7)
		if c < 0x80 {
			return x, off, nil
		}
	}
}

// FNV-1a 64 位（与 hash/fnv 同参数，本包不依赖 compress）。
const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// Checksum 对 data 增量更新 FNV-1a，返回新值。
func Checksum(sum uint64, data []byte) uint64 {
	for _, c := range data {
		sum ^= uint64(c)
		sum *= fnvPrime64
	}
	return sum
}

// NewChecksum 返回 FNV-1a 初值。
func NewChecksum() uint64 { return fnvOffset64 }

// Header 编码流头。
func Header(winCap int) []byte {
	b := []byte{TagHeader}
	b = AppendUvarint(b, uint64(Version))
	return AppendUvarint(b, uint64(winCap))
}
