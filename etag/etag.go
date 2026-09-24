// Package etag 解析实体标签（entity-tag）并提供强比较与弱比较。
// 本包不依赖其他任何包。
package etag

import (
	"errors"
	"strings"
)

// ETag 表示一个实体标签；Weak 为 true 表示带 W/ 前缀的弱标签。
type ETag struct {
	Weak bool
	Tag  string // 引号内的 opaque-tag，不含引号
}

// ErrSyntax 表示实体标签或标签列表语法错误。
var ErrSyntax = errors.New("etag: 语法错误")

// StrongEqual 强比较：双方都不是弱标签且 opaque-tag 逐字节相等。
func StrongEqual(a, b ETag) bool {
	return !a.Weak && !b.Weak && a.Tag == b.Tag
}

// WeakEqual 弱比较：只比较 opaque-tag，忽略任何一方的 W/ 前缀。
func WeakEqual(a, b ETag) bool { return a.Tag == b.Tag }

// Parse 解析单个实体标签，例如 `"x"` 或 `W/"x"`；允许首尾空白。
func Parse(s string) (ETag, error) {
	s = strings.Trim(s, " \t\r\n")
	var e ETag
	if strings.HasPrefix(s, "W/") {
		e.Weak = true
		s = s[2:]
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return ETag{}, ErrSyntax
	}
	e.Tag = s[1 : len(s)-1]
	if e.Tag == "" || strings.ContainsAny(e.Tag, "\"\r\n") {
		return ETag{}, ErrSyntax
	}
	return e, nil
}

// ParseList 解析逗号分隔的实体标签列表。
// 当 s 是 "*" 时 star 为 true、tags 为 nil。
// 列表项之间允许空格、制表符与折行空白（CRLF 后接空白）。
// 空列表（只有逗号或空白）是语法错误。
func ParseList(s string) (tags []ETag, star bool, err error) {
	if strings.Trim(s, " \t\r\n") == "*" {
		return nil, true, nil
	}
	for _, part := range strings.Split(s, ",") {
		e, err := Parse(part)
		if err != nil {
			return nil, false, err
		}
		tags = append(tags, e)
	}
	return tags, false, nil
}
