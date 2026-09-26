// Package codec 提供 Base64 定长块随机访问。依赖 b64。
package codec

import (
	"errors"
	"sync/atomic"

	"ontology/b64"
)

// ErrOutOfRange 表示块下标越界。
var ErrOutOfRange = errors.New("codec: 块下标越界")

// lastRead 记录最近一次 DecodeBlockAt 为定位第 k 个块而读取的输入字节数。
// 非导出、不出现在公开接口：仅本包内部测试可直接读取。
var lastRead atomic.Int64

// BlockCount 返回 data 中的 4 字符块数。
func BlockCount(data []byte) int { return len(data) / 4 }

// DecodeBlockAt 解出第 k 个 4 字符块（末块按填充规则返回 1/2 字节）。
// 定长寻址 O(1)：直接跳到偏移 4k，只读这 4 个字节，与块总数无关。
func DecodeBlockAt(data []byte, k int) ([]byte, error) {
	if len(data)%4 != 0 {
		return nil, b64.ErrLength
	}
	if k < 0 || k >= BlockCount(data) {
		return nil, ErrOutOfRange
	}
	lastRead.Store(4)
	return b64.Decode(data[4*k : 4*k+4])
}
