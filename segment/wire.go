package segment

// 自定义极简二进制助手：定宽整数小端、zigzag varint、长度前缀。
// 不使用 gob/json。读取侧全部走 reader，任意越界都返回错误而非 panic。

import "io"

func putU64(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * i))
	}
}

func putU32(b []byte, v uint32) {
	for i := 0; i < 4; i++ {
		b[i] = byte(v >> (8 * i))
	}
}

// zig 把有符号数双射为非负数（0->0,-1->1,1->2,...）。
func zig(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

func unzig(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

func putVarint(buf *[]byte, u uint64) {
	for u >= 0x80 {
		*buf = append(*buf, byte(u)|0x80)
		u >>= 7
	}
	*buf = append(*buf, byte(u))
}

// reader 是有界读取器；remaining 不足时所有方法返回 io.ErrUnexpectedEOF。
type reader struct {
	b   []byte
	pos int
}

func (r *reader) byte1() (byte, error) {
	if r.pos+1 > len(r.b) {
		return 0, io.ErrUnexpectedEOF
	}
	v := r.b[r.pos]
	r.pos++
	return v, nil
}

func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.b) {
		return nil, io.ErrUnexpectedEOF
	}
	v := r.b[r.pos : r.pos+n]
	r.pos += n
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

func (r *reader) u64() (uint64, error) {
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(b[i]) << (8 * i)
	}
	return v, nil
}

func (r *reader) varint() (uint64, error) {
	var u uint64
	for shift := 0; shift < 64; shift += 7 {
		c, err := r.byte1()
		if err != nil {
			return 0, err
		}
		u |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return u, nil
		}
}
	return 0, io.ErrUnexpectedEOF
}
