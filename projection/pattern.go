package projection

import (
	"errors"
	"fmt"
	"strings"
)

// wildcard 是单层通配符，只允许作为模式的最后一段。
const wildcard = "*"

// pattern 是编译后的字段匹配模式，形如 "addr.city" 或 "addr.*"。
type pattern struct {
	raw      string
	segments []string
}

// parsePattern 解析并校验模式原文，返回编译后的模式。
func parsePattern(raw string) (pattern, error) {
	if raw == "" {
		return pattern{}, errors.New("空模式")
	}
	segments := strings.Split(raw, ".")
	for i, seg := range segments {
		if seg == "" {
			return pattern{}, fmt.Errorf("第 %d 段为空", i+1)
		}
		if seg == wildcard {
			if i != len(segments)-1 {
				return pattern{}, errors.New("通配符 * 只能出现在最后一段，不支持多层通配")
			}
			continue
		}
		if strings.Contains(seg, "*") {
			return pattern{}, fmt.Errorf("段 %q 中通配符必须独占一段", seg)
		}
		if !validSegment(seg) {
			return pattern{}, fmt.Errorf("段 %q 含非法字符", seg)
		}
	}
	return pattern{raw: raw, segments: segments}, nil
}

// validSegment 校验字段名段：字母、数字、下划线、连字符。
func validSegment(seg string) bool {
	for _, r := range seg {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// specificity 返回模式的具体度：精确段越多越具体。
// 由于通配符只允许在末段，能命中同一路径的模式其具体度必然全序。
func (p pattern) specificity() int {
	n := 0
	for _, seg := range p.segments {
		if seg != wildcard {
			n++
		}
	}
	return n
}

// matches 判断模式是否命中给定字段路径。
// 段数必须相等：addr.* 只匹配 addr 的直接子字段，不跨层。
func (p pattern) matches(path []string) bool {
	if len(p.segments) != len(path) {
		return false
	}
	for i, seg := range p.segments {
		if seg == wildcard {
			continue
		}
		if seg != path[i] {
			return false
		}
	}
	return true
}
