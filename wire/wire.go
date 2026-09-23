package wire

import "errors"

// 记录类型 tag（低 2 位）。
const (
	TagLit   = 0
	TagRef   = 1
	TagFlush = 2
	TagEnd   = 3
)

// Version 为流格式版本。
const Version = 1

// ErrTruncated 表示压缩流在记录中途结束（截断）。
var ErrTruncated = errors.New("wire: truncated stream")

// ErrVarint 表示变长整数超长（>10 字节）或溢出 uint64。
var ErrVarint = errors.New("wire: varint too long or overflow")

// Reader 在字节片段上做游标式读取。
type Reader struct {
	B []byte
	P int // 下一个待读字节在流中的绝对偏移
}

// Byte 读取一个字节；数据不足返回 ErrTruncated（offset 为截断点）。
func (r *Reader) Byte() (byte, int, error) {
	if r.P >= len(r.B) {
		return 0, r.P, ErrTruncated
	}
	b := r.B[r.P]
	r.P++
	return b, r.P - 1, nil
}

// Bytes 读取 n 个字节；不足返回 ErrTruncated。
func (r *Reader) Bytes(n int) ([]byte, int, error) {
	if n < 0 || r.P+n > len(r.B) {
		return nil, r.P, ErrTruncated
	}
	s := r.B[r.P : r.P+n]
	r.P += n
	return s, r.P - n, nil
}

// Varint 读取无符号 LEB128；超过 10 字节或溢出返回 ErrVarint。
func (r *Reader) Varint() (uint64, int, error) {
	start := r.P
	var x uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, _, err := r.Byte()
		if err != nil {
			return 0, start, err
		}
		if shift == 63 && b > 1 {
			return 0, start, ErrVarint
		}
		x |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return x, start, nil
		}
	}
	return 0, start, ErrVarint
}

// Tagged 读取「首字节：低 2 位 tag、高 6 位为首个 varint 段」并还原该 varint。
// 对纯标记（FLUSH/END）返回 tag、值为 0。
func (r *Reader) Tagged() (tag int, val uint64, offset int, err error) {
	b, off, err := r.Byte()
	if err != nil {
		return 0, 0, off, err
	}
	tag = int(b & 3)
	if tag == TagFlush || tag == TagEnd {
		return tag, 0, off, nil
	}
	x := uint64(b >> 2)
	if b&0x80 == 0 {
		return tag, x, off, nil
	}
	shift := uint(6)
	for {
		c, _, e := r.Byte()
		if e != nil {
			return 0, 0, off, e
		}
		if shift >= 64 {
			return 0, 0, off, ErrVarint
		}
		bits := 64 - shift
		if bits < 7 {
			if c&0x80 != 0 || c>>bits != 0 {
				return 0, 0, off, ErrVarint
			}
			x |= uint64(c) << shift
			break
		}
		x |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			break
		}
		shift += 7
	}
	return tag, x, off, nil
}

// PutVarint 追加编码无符号 LEB128。
func PutVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// PutTagged 追加「tag 与首个 varint 共占首字节」的编码。
func PutTagged(dst []byte, tag int, v uint64) []byte {
	first := byte(v&0x3f)<<2 | byte(tag)
	v >>= 6
	if v == 0 {
		return append(dst, first)
	}
	dst = append(dst, first|0x80)
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// FNVOffset 是 FNV-1a 64 的初始值。
const FNVOffset = fnvOffset

// FNV 以给定初值继续累加 FNV-1a 64，供增量校验。
func FNV(h uint64, p []byte) uint64 {
	for _, b := range p {
		h ^= uint64(b)
		h *= fnvPrime
	}
	return h
}
