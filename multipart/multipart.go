// Package multipart 把若干区间字节封装为 multipart/byteranges 响应体。
// 边界串在内容确定之后生成，并扫描全部内容确保不冲突。
package multipart

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"ontology/coalesce"
)

// ErrBoundaryRetries 表示边界串生成重试次数超限（与内容反复冲突）。
var ErrBoundaryRetries = errors.New("multipart: boundary generation retries exhausted")

const boundaryPrefix = "ontology-boundary-"

// Build 用密码学随机边界串组装完整 multipart 响应体，见 BuildWith。
func Build(contentType string, total int64, ranges []coalesce.Range, contents [][]byte, maxRetries int) (body []byte, boundary string, err error) {
	return BuildWith(func() string { return boundaryPrefix + randomHex(16) },
		contentType, total, ranges, contents, maxRetries)
}

// BuildWith 组装完整 multipart 响应体。contents[i] 是 ranges[i] 对应的字节，
// gen 产生候选边界串。返回响应体与所选边界串；重试 maxRetries 次仍冲突时
// 返回 ErrBoundaryRetries。
func BuildWith(gen func() string, contentType string, total int64, ranges []coalesce.Range, contents [][]byte, maxRetries int) (body []byte, boundary string, err error) {
	if len(ranges) != len(contents) {
		return nil, "", fmt.Errorf("multipart: %d ranges but %d contents", len(ranges), len(contents))
	}
	if len(ranges) == 0 {
		return nil, "", errors.New("multipart: no parts")
	}
	if maxRetries <= 0 {
		maxRetries = 8
	}
	for attempt := 0; attempt < maxRetries; attempt++ {
		boundary = gen()
		if conflicts(contents, boundary) {
			continue
		}
		return assemble(contentType, total, ranges, contents, boundary), boundary, nil
	}
	return nil, "", ErrBoundaryRetries
}

// randomHex 返回 n 字节密码学随机的 hex 串（2n 个字符）。
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand 失败无法恢复
	}
	return hex.EncodeToString(buf)
}

// conflicts 判断分隔符 "\r\n--boundary" 是否会与任何一段内容产生歧义：
// 把内容与其后真实分隔符拼接，分隔符出现次数必须恰好为 1（即末尾那次），
// 同时覆盖"内容内部包含"与"内容后缀与分隔符前缀重叠"两种冲突。
func conflicts(contents [][]byte, boundary string) bool {
	delim := []byte("\r\n--" + boundary)
	for _, c := range contents {
		joined := make([]byte, 0, len(c)+len(delim))
		joined = append(joined, c...)
		joined = append(joined, delim...)
		if bytes.Count(joined, delim) != 1 {
			return true
		}
	}
	return false
}

// assemble 按 RFC 2046 分隔符格式拼接：每段以 "\r\n--boundary" 开头，
// 带 Content-Type 与 Content-Range 头，以 "\r\n--boundary--\r\n" 结束。
func assemble(contentType string, total int64, ranges []coalesce.Range, contents [][]byte, boundary string) []byte {
	var buf bytes.Buffer
	for i, r := range ranges {
		fmt.Fprintf(&buf, "\r\n--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", contentType)
		fmt.Fprintf(&buf, "Content-Range: bytes %d-%d/%d\r\n\r\n", r.Start, r.End-1, total)
		buf.Write(contents[i])
	}
	fmt.Fprintf(&buf, "\r\n--%s--\r\n", boundary)
	return buf.Bytes()
}
