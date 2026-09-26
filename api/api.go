// Package api 是对外面层：字符串级编解码与内置自检。
package api

import (
	"bytes"
	"fmt"

	"ontology/enc"
	"ontology/stream"
)

type API struct{}

func New() *API { return &API{} }

// EncodeString 严格解码 s 的每个 rune 再重编码；s 含非法 UTF-8 时整体报错。
func (a *API) EncodeString(s string) ([]byte, error) {
	rs, err := stream.DecodeAll([]byte(s))
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, r := range rs {
		b, err := enc.EncodeRune(r)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// DecodeString 整段严格解码为 string；任一非法字节即整体报错。
func (a *API) DecodeString(b []byte) (string, error) {
	rs, err := stream.DecodeAll(b)
	if err != nil {
		return "", err
	}
	return string(rs), nil
}

// SelfCheck 对内置向量核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	vectors := []rune{0x00, 0x41, 0x7F, 0x80, 0xA2, 0x7FF, 0x800, 0x20AC, 0xFFFF, 0x10000, 0x1F600, 0x10FFFF}
	for _, r := range vectors { // 不变量 1：往返一致
		b, err := enc.EncodeRune(r)
		if err != nil {
			return fmt.Errorf("selfcheck encode U+%04X: %w", r, err)
		}
		got, n, err := enc.DecodeRune(b)
		if err != nil || got != r || n != len(b) {
			return fmt.Errorf("selfcheck roundtrip U+%04X: got U+%04X n=%d err=%v", r, got, n, err)
		}
	}
	bad := [][]byte{ // 不变量 2：六类非法输入均被拒
		{0x80}, {0xFF}, {0xC0, 0xAF}, {0xED, 0xA0, 0x80},
		{0xF4, 0x90, 0x80, 0x80}, {0xE2, 0x82}, {0xE2, 0x28, 0xAC},
	}
	for _, b := range bad {
		if _, _, err := enc.DecodeRune(b); err == nil {
			return fmt.Errorf("selfcheck: % X 未被拒绝", b)
		}
	}
	s, err := a.DecodeString([]byte("A¢€😀")) // 经 api 的端到端往返
	if err != nil || s != "A¢€😀" {
		return fmt.Errorf("selfcheck api roundtrip: %q %v", s, err)
	}
	back, err := a.EncodeString(s)
	if err != nil || !bytes.Equal(back, []byte("A¢€😀")) {
		return fmt.Errorf("selfcheck api encode: % X %v", back, err)
	}
	return nil
}
