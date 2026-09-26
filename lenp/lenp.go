// Package lenp 提供 4 字节小端无符号长度前缀的读写。
package lenp

import (
	"encoding/binary"
	"errors"
)

// ErrShortPrefix 缓冲不足 4 字节，读不出完整长度前缀。
var ErrShortPrefix = errors.New("lenp: need 4 bytes for length prefix")

// Len 长度前缀占用的字节数。
const Len = 4

// PutLength 返回 n 的 4 字节小端编码。
func PutLength(n int) []byte {
	b := make([]byte, Len)
	binary.LittleEndian.PutUint32(b, uint32(n))
	return b
}

// GetLength 从 b 前 4 字节读出小端长度；不足 4 字节报 ErrShortPrefix。
func GetLength(b []byte) (int, error) {
	if len(b) < Len {
		return 0, ErrShortPrefix
	}
	return int(binary.LittleEndian.Uint32(b)), nil
}
