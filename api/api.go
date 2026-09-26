// Package api 对外门面：字符串级编解码与自检。依赖 stream（间接依赖 enc）。
package api

import (
	"errors"
	"fmt"

	"ontology/enc"
	"ontology/stream"
)

// Codec 无状态，可并发使用。
type Codec struct{}

func New() *Codec { return &Codec{} }

// EncodeString 把字符串逐 rune 编成 UTF-8 字节序列。
func (c *Codec) EncodeString(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		b, err := enc.EncodeRune(r)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// DecodeString 严格整段解码；任一字节非法即整体报错。
func (c *Codec) DecodeString(b []byte) (string, error) {
	rs, err := stream.DecodeAll(b)
	if err != nil {
		return "", err
	}
	return string(rs), nil
}

// refEncode 手写教科书参照：按码点范围逐段取位、拼前缀。
func refEncode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | (byte(r>>0) & 0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | (byte(r>>6) & 0x3F), 0x80 | (byte(r>>0) & 0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | (byte(r>>12) & 0x3F), 0x80 | (byte(r>>6) & 0x3F), 0x80 | (byte(r>>0) & 0x3F)}
	}
}

var vecRunes = []rune{0x00, 0x41, 0x7F, 0x80, 0xA2, 0x7FF, 0x800, 0x20AC, 0xFFFD, 0x10000, 0x1F600, 0x10FFFF}

var vecBad = []struct {
	b []byte
	e error
}{
	{[]byte{0x80}, enc.ErrInvalidLead},
	{[]byte{0xFF}, enc.ErrInvalidLead},
	{[]byte{0xC0, 0xAF}, enc.ErrOverlong},
	{[]byte{0xE0, 0x80, 0xAF}, enc.ErrOverlong},
	{[]byte{0xED, 0xA0, 0x80}, enc.ErrSurrogate},
	{[]byte{0xF4, 0x90, 0x80, 0x80}, enc.ErrOutOfRange},
	{[]byte{0xE2, 0x82}, enc.ErrTruncated},
	{[]byte{0xE2, 0x28, 0xAC}, enc.ErrBadContinuation},
}

// SelfCheck 对内置向量核验四条不变量：往返一致、严格拒绝、
// 与朴素参照一致、失败不留痕。全部通过返回 nil。
func (c *Codec) SelfCheck() error {
	for _, r := range vecRunes { // 不变量 1 + 3
		b, err := enc.EncodeRune(r)
		if err != nil {
			return fmt.Errorf("selfcheck encode U+%04X: %w", r, err)
		}
		if string(b) != string(refEncode(r)) {
			return fmt.Errorf("selfcheck U+%04X: mismatch with reference", r)
		}
		got, n, err := enc.DecodeRune(b)
		if err != nil || got != r || n != len(b) {
			return fmt.Errorf("selfcheck roundtrip U+%04X: got U+%04X n=%d err=%v", r, got, n, err)
		}
	}
	for _, v := range vecBad { // 不变量 2
		if _, _, err := enc.DecodeRune(v.b); !errors.Is(err, v.e) {
			return fmt.Errorf("selfcheck reject % X: want %v, got %v", v.b, v.e, err)
		}
	}
	rd := stream.NewReader([]byte{0xC0, 0xAF}) // 不变量 4
	if _, err := rd.Next(); err == nil || rd.Pos() != 0 {
		return fmt.Errorf("selfcheck cursor moved on rejected Next: pos=%d", rd.Pos())
	}
	return nil
}
