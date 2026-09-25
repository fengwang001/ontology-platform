// Package wire 定义自定 LZ77 压缩流的字节格式。不依赖其他包。
package wire

import "errors"

const (
	TagLiteral byte = 0 // 后接 varint n 与 n 个字面量字节
	TagMatch   byte = 1 // 后接 varint distance、varint length
	TagFlush   byte = 2 // 刷新标记，无负载
	TagEnd     byte = 3 // 后接 varint 原文总长、varint FNV-1a 64 校验和

	Version byte = 1
)

// 损坏类错误（哨兵），偏移信息由 CorruptError 承载。
var (
	ErrMagic         = errors.New("wire: bad magic")
	ErrVersion       = errors.New("wire: unsupported version")
	ErrZeroDistance  = errors.New("wire: zero back-reference distance")
	ErrDistOutput    = errors.New("wire: distance exceeds bytes produced")
	ErrDistWindow    = errors.New("wire: distance exceeds window capacity")
	ErrVarintTooLong = errors.New("wire: varint longer than 10 bytes or overflows uint64")
	ErrLengthMismatch = errors.New("wire: tail length does not match output")
	ErrChecksum      = errors.New("wire: checksum mismatch")
	ErrTrailingBytes = errors.New("wire: trailing bytes after stream end")
	ErrTruncated     = errors.New("wire: truncated stream")
	ErrBadTag        = errors.New("wire: unknown record tag")
	ErrLengthSmall   = errors.New("wire: match length below minimum")
)

// CorruptError 包装具体哨兵错误并给出压缩流中的字节偏移。
type CorruptError struct {
	Offset int
	Err    error
}

func (e *CorruptError) Error() string { return e.Err.Error() + " at offset " + itoa(e.Offset) }
func (e *CorruptError) Unwrap() error { return e.Err }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// AppendUvarint 追加 LEB128 无符号变长整数。
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint 从 src[off:] 解析变长整数，返回值与新偏移；
// 数据不足返回 ErrTruncated，超长/溢出返回 ErrVarintTooLong。
func ReadUvarint(src []byte, off int) (uint64, int, error) {
	var x uint64
	var s uint
	start := off
	for i := 0; ; i++ {
		if off >= len(src) {
			return 0, off, ErrTruncated
		}
		b := src[off]
		off++
		if i >= 10 || i == 9 && b > 1 {
			return 0, start, ErrVarintTooLong
		}
		if b < 0x80 {
			if i == 9 && b > 1 {
				return 0, start, ErrVarintTooLong
			}
			return x | uint64(b)<<s, off, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
}

var magic = [3]byte{'L', 'Z', '7'}

// HeaderLen 是固定流头长度。
const HeaderLen = 4

// AppendHeader 写入流头。
func AppendHeader(dst []byte) []byte {
	return append(dst, magic[0], magic[1], magic[2], Version)
}

// CheckHeader 在流起始处校验流头；出错时偏移指向流头。
func CheckHeader(src []byte) error {
	if len(src) < HeaderLen {
		return ErrTruncated
	}
	if src[0] != magic[0] || src[1] != magic[1] || src[2] != magic[2] {
		return &CorruptError{Offset: 0, Err: ErrMagic}
	}
	if src[3] != Version {
		return &CorruptError{Offset: 3, Err: ErrVersion}
	}
	return nil
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// Hasher 是增量 FNV-1a 64 校验和（同时满足 io.Writer）。
type Hasher uint64

func NewHasher() *Hasher { h := Hasher(fnvOffset); return &h }

func (h *Hasher) Write(p []byte) (int, error) {
	v := uint64(*h)
	for _, b := range p {
		v = (v ^ uint64(b)) * fnvPrime
	}
	*h = Hasher(v)
	return len(p), nil
}

// AddByte 无分配地更新一个字节的校验和。
func (h *Hasher) AddByte(b byte) {
	*h = Hasher((uint64(*h) ^ uint64(b)) * fnvPrime)
}

func (h *Hasher) Sum64() uint64 { return uint64(*h) }
