// Package pack 按 layout.Schema 把字段值打包成定长小端字节串，或解回。
package pack

import (
	"errors"
	"fmt"

	"ontology/layout"
)

var (
	// ErrUnknownField 输入里出现 schema 没有的字段名。
	ErrUnknownField = errors.New("pack: unknown field")
	// ErrMissingField 输入缺少 schema 要求的字段。
	ErrMissingField = errors.New("pack: missing field")
	// ErrOverflow 字段值超出其有符号/无符号值域。
	ErrOverflow = errors.New("pack: value out of range")
	// ErrShortBuffer 解包缓冲长度不足 schema 总长。
	ErrShortBuffer = errors.New("pack: buffer too short")
)

// bounds 返回宽度 w 字节、符号性 signed 的字段值域 [lo, hi]。
func bounds(w int, signed bool) (lo, hi int64) {
	bits := uint(8 * w)
	if signed {
		return -(1 << (bits - 1)), 1<<(bits-1) - 1
	}
	if bits >= 64 {
		return 0, 1<<63 - 1 // int64 能表示的最大无符号低位
	}
	return 0, 1<<bits - 1
}

// Pack 把 fields 按 schema 打包为定长字节串。任何字段非法即整体失败。
func Pack(s *layout.Schema, fields map[string]int64) ([]byte, error) {
	// 先全量校验，再写一个字节：失败不留痕。
	type item struct {
		f layout.Field
		v int64
	}
	items := make([]item, 0, len(fields))
	for name, v := range fields {
		f, ok := s.Field(name)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownField, name)
		}
		if lo, hi := bounds(f.Width, f.Signed); v < lo || v > hi {
			return nil, fmt.Errorf("%w: %q=%d", ErrOverflow, name, v)
		}
		items = append(items, item{f, v})
	}
	for _, f := range s.Fields() {
		if _, ok := fields[f.Name]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrMissingField, f.Name)
		}
	}
	buf := make([]byte, s.Total())
	for _, it := range items {
		u := uint64(it.v)
		for i := 0; i < it.f.Width; i++ {
			buf[it.f.Offset+i] = byte(u >> (8 * i))
		}
	}
	return buf, nil
}

// Unpack 从 buf 按 schema 解出全部字段。缓冲不足即整体失败。
func Unpack(s *layout.Schema, buf []byte) (map[string]int64, error) {
	if len(buf) < s.Total() {
		return nil, fmt.Errorf("%w: have %d, need %d", ErrShortBuffer, len(buf), s.Total())
	}
	out := make(map[string]int64, len(s.Fields()))
	for _, f := range s.Fields() {
		var u uint64
		for i := 0; i < f.Width; i++ {
			u |= uint64(buf[f.Offset+i]) << (8 * i)
		}
		if f.Signed && f.Width < 8 && u&(1<<(uint(8*f.Width)-1)) != 0 {
			u |= ^uint64(0) << uint(8*f.Width) // 符号扩展
		}
		out[f.Name] = int64(u)
	}
	return out, nil
}
