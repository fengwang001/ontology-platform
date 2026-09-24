// Package multipart 实现 multipart/byteranges 的分段封装：
// 边界串生成、每段头部、结束边界。不依赖 mime/multipart。
//
// 边界串安全策略：随机生成（128 bit 熵）+ 显式检查不出现在内容字节里
// + 有限次重试。显式检查使冲突成为确定性不可能，随机性只降低重试概率。
package multipart

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"ontology/coalesce"
)

// Prefix 是边界串的固定前缀。
const Prefix = "ontology-"

// ErrBoundaryRetries 表示边界串生成重试次数耗尽。
var ErrBoundaryRetries = errors.New("multipart: boundary generation retries exhausted")

// Boundary 生成一个不出现在 avoid 字节序列中的边界串。
// rng 提供随机源（生产用 crypto/rand.Reader，测试可注入确定性源）。
// 最多尝试 maxTries 次（<=0 视为 1 次），全部命中返回 ErrBoundaryRetries。
func Boundary(avoid []byte, maxTries int, rng io.Reader) (string, error) {
	if maxTries <= 0 {
		maxTries = 1
	}
	for i := 0; i < maxTries; i++ {
		var raw [16]byte
		if _, err := io.ReadFull(rng, raw[:]); err != nil {
			return "", err
		}
		b := Prefix + hex.EncodeToString(raw[:])
		if !bytes.Contains(avoid, []byte(b)) {
			return b, nil
		}
	}
	return "", ErrBoundaryRetries
}

// Header 返回一个分段的头部字节（含前导边界行）。
func Header(boundary, contentType string, r coalesce.Range, total int64) []byte {
	return fmt.Appendf(nil, "--%s\r\nContent-Type: %s\r\nContent-Range: bytes %d-%d/%d\r\n\r\n",
		boundary, contentType, r.Start, r.End, total)
}

// Separator 返回相邻两段之间的分隔字节。
func Separator() []byte { return []byte("\r\n") }

// Closing 返回结束边界字节。
func Closing(boundary string) []byte {
	return fmt.Appendf(nil, "--%s--\r\n", boundary)
}
