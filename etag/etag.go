// Package etag 解析 HTTP 实体标签并提供强/弱两种比较。
// 不依赖本仓库其他包。
package etag

import (
	"errors"
	"strings"
)

// ErrSyntax 表示实体标签或标签列表存在语法错误（含空列表项）。
var ErrSyntax = errors.New("etag: syntax error")

// Tag 是一个已解析的实体标签：不透明值加弱标签标记。
type Tag struct {
	Weak  bool
	Value string
}

// StrongEqual 强比较：双方都不是弱标签且值相等才相等。
func StrongEqual(a, b Tag) bool {
	return !a.Weak && !b.Weak && a.Value == b.Value
}

// WeakEqual 弱比较：只看不透明值，忽略弱标签标记。
func WeakEqual(a, b Tag) bool {
	return a.Value == b.Value
}

// Parse 解析单个实体标签，形如 `"abc"` 或 `W/"abc"`。
func Parse(s string) (Tag, error) {
	body := s
	weak := false
	if strings.HasPrefix(body, "W/") {
		weak = true
		body = body[2:]
	}
	if len(body) < 2 || body[0] != '"' || body[len(body)-1] != '"' {
		return Tag{}, ErrSyntax
	}
	value := body[1 : len(body)-1]
	if strings.ContainsAny(value, "\"\r\n") {
		return Tag{}, ErrSyntax
	}
	return Tag{Weak: weak, Value: value}, nil
}

// ParseList 解析逗号分隔的实体标签列表（If-Match / If-None-Match 的值）。
// 列表恰为 "*" 时返回 star=true；"*" 与其他项混排、空列表项（含只有
// 逗号的空列表）均为语法错误。项间允许空白与 CRLF 折行。
func ParseList(s string) (tags []Tag, star bool, err error) {
	s = strings.ReplaceAll(s, "\r\n", "")
	parts := strings.Split(s, ",")
	if len(parts) == 1 && strings.Trim(parts[0], " \t") == "*" {
		return nil, true, nil
	}
	for _, p := range parts {
		p = strings.Trim(p, " \t")
		if p == "" {
			return nil, false, ErrSyntax
		}
		t, err := Parse(p)
		if err != nil {
			return nil, false, err
		}
		tags = append(tags, t)
	}
	return tags, false, nil
}
