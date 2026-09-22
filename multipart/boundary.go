package multipart

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
)

// ErrBoundaryAttempts 在边界串反复与内容冲突、达到重试上限时返回。
var ErrBoundaryAttempts = errors.New("multipart: could not choose a boundary absent from content within retry limit")

const boundaryPrefix = "ontologyBR"
const boundaryRandomBytes = 24 // 192 位随机量，base64url 后 32 字符

// ChooseBoundary 生成一个不会出现在 body 任何位置的边界串。
//
// 每次用 crypto/rand 取 24 字节并做 base64rawurl 编码，前缀 "ontologyBR"
// 保证它不是内容中自然出现的常见串。判定标准是带前导 "--" 的定界行
// ("--boundary") 绝不出现在 body 中；这正是解析器识别分段的唯一锚点。
// maxAttempts 次内都冲突则返回 ErrBoundaryAttempts，不改变调用方状态。
func ChooseBoundary(body []byte, maxAttempts int) (string, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var raw [boundaryRandomBytes]byte
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		candidate := boundaryPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
		if !containsDelimiter(body, candidate) {
			return candidate, nil
		}
	}
	return "", ErrBoundaryAttempts
}

// ContainsBoundary 报告 body 中是否出现该边界的定界行（含前导 "--"）。
func ContainsBoundary(body []byte, boundary string) bool {
	return containsDelimiter(body, boundary)
}

func containsDelimiter(body []byte, boundary string) bool {
	needle := []byte("--" + boundary)
	return indexBytes(body, needle) >= 0
}

func indexBytes(haystack, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
