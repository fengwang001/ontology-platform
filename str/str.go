// Package str 提供 Ontology 字段名的字符串校验，哨兵错误可用 errors.Is 区分。
package str

import (
	"errors"

	"ontology/win"
)

// 三类哨兵错误，均可用 errors.Is 区分。
var (
	ErrEmpty   = errors.New("str: empty field name")
	ErrInvalid = errors.New("str: byte outside printable ASCII")
	ErrTooLong = errors.New("str: longest distinct run exceeds limit")
)

// ValidateField 校验字段名：非空、可打印 ASCII、且最长无重复字符
// 子串长度不超过 limit（用于合法性校验的边界判定）。
func ValidateField(name string, limit int) error {
	if name == "" {
		return ErrEmpty
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 || name[i] > 0x7e {
			return ErrInvalid
		}
	}
	if win.LongestSubstring(name) > limit {
		return ErrTooLong
	}
	return nil
}
