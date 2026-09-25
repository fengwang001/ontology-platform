// Package str 校验 Ontology 字段名并暴露哨兵错误。
package str

import (
	"errors"
	"unicode/utf8"

	"ontology/win"
)

// 三类校验失败的哨兵错误，可用 errors.Is 区分。
var (
	ErrEmpty       = errors.New("str: empty field name")
	ErrInvalidUTF8 = errors.New("str: invalid utf-8")
	ErrTooLong     = errors.New("str: field name too long")
)

const maxLen = 128

// Validate 校验字段名，失败时返回上述哨兵错误之一。
func Validate(name string) error {
	if name == "" {
		return ErrEmpty
	}
	if !utf8.ValidString(name) {
		return ErrInvalidUTF8
	}
	if len(name) > maxLen {
		return ErrTooLong
	}
	return nil
}

// MaxUniqueRun 校验字段名并返回其最长无重复字符子串长度。
func MaxUniqueRun(name string) (int, error) {
	if err := Validate(name); err != nil {
		return 0, err
	}
	return win.LongestSubstring(name), nil
}
