// Package codec 是对外门面：字符串编解码与自检。
package codec

import (
	"bytes"
	"fmt"

	"ontology/b32"
)

// EncodeString 把 s 编码为 base32 字符串。
func EncodeString(s string) string { return b32.Encode([]byte(s)) }

// DecodeString 解码 base32 字符串；非法输入整体失败并返回可判定错误。
func DecodeString(s string) (string, error) {
	b, err := b32.Decode(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SelfCheck 对长度 0 到 20 的输入做 编码->解码->比对，
// 并核验每个编码结果的长度与填充个数合法。
func SelfCheck() error {
	legalPad := map[int]bool{0: true, 1: true, 3: true, 4: true, 6: true}
	for n := 0; n <= 20; n++ {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(i*11 + n)
		}
		enc := b32.Encode(src)
		if len(enc) != 8*((n+4)/5) {
			return fmt.Errorf("n=%d: encoded length %d", n, len(enc))
		}
		pad := 0
		for pad < len(enc) && enc[len(enc)-1-pad] == '=' {
			pad++
		}
		if !legalPad[pad] {
			return fmt.Errorf("n=%d: illegal padding %d", n, pad)
		}
		dec, err := b32.Decode(enc)
		if err != nil {
			return fmt.Errorf("n=%d: decode: %w", n, err)
		}
		if !bytes.Equal(dec, src) {
			return fmt.Errorf("n=%d: roundtrip mismatch", n)
		}
	}
	return nil
}
