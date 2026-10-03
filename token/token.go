// Package token 按单个 ASCII 空格切分日志行并把含数字的词掩码为 "<*>"。
package token

import "errors"

const Wildcard = "<*>"

const (
	maxWords    = 32
	maxWordSize = 64
)

// ErrInvalidMessage 表示日志行不满足切分约束。
var ErrInvalidMessage = errors.New("token: invalid message")

// ValidTenant 报告租户串是否合法：非空且不超过 64 字节。
func ValidTenant(tenant string) bool {
	return len(tenant) > 0 && len(tenant) <= 64
}

// Tokenize 按单空格切分 msg 并掩码数字词。
// msg 不允许首尾空格、连续空格（即不允许空词），
// 词数须在 1..32，每词不超过 64 字节，否则返回错误。
func Tokenize(msg string) ([]string, error) {
	if len(msg) == 0 || msg[0] == ' ' || msg[len(msg)-1] == ' ' {
		return nil, ErrInvalidMessage
	}
	tokens := make([]string, 0, 8)
	start := 0
	for i := 0; i <= len(msg); i++ {
		if i < len(msg) && msg[i] != ' ' {
			continue
		}
		word := msg[start:i]
		if word == "" || len(word) > maxWordSize {
			return nil, ErrInvalidMessage
		}
		tokens = append(tokens, mask(word))
		start = i + 1
	}
	if len(tokens) == 0 || len(tokens) > maxWords {
		return nil, ErrInvalidMessage
	}
	return tokens, nil
}

// mask 在词含任一 ASCII 数字字符时返回通配符，否则原样返回。
func mask(word string) string {
	for i := 0; i < len(word); i++ {
		if word[i] >= '0' && word[i] <= '9' {
			return Wildcard
		}
	}
	return word
}
