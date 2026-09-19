package ontology

import (
	"fmt"
	"strings"
)

// Pattern 是一条已解析的字段路径模式。
// 合法形态为精确名（a.b.c）或结尾单层通配（a.*）。
// 通配只代表其所在层的任意一个字段，不跨层。
type Pattern struct {
	raw  string
	segs []string
	wild bool
}

// Raw 返回规则原文。
func (p Pattern) Raw() string { return p.raw }

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// parsePattern 校验并解析模式。非法情形包括：空模式、空段、
// 非法字符、以及非末段的 *（含 a.*.b 这类多层通配）。
func parsePattern(raw string) (Pattern, error) {
	if strings.TrimSpace(raw) == "" {
		return Pattern{}, fmt.Errorf("empty pattern")
	}
	segs := strings.Split(raw, ".")
	for i, seg := range segs {
		if seg == "*" {
			// 通配符必须独占一段，且只能出现在最后一段。
			if i != len(segs)-1 {
				return Pattern{}, fmt.Errorf("invalid pattern %q: wildcard is only allowed as the final segment", raw)
			}
			continue
		}
		if !validIdent(seg) {
			return Pattern{}, fmt.Errorf("invalid pattern %q: illegal field name %q", raw, seg)
		}
	}
	return Pattern{
		raw:  raw,
		segs: segs,
		wild: segs[len(segs)-1] == "*",
	}, nil
}

// match 判断模式是否命中给定路径（等长匹配，通配只代表单层）。
func (p Pattern) match(path []string) bool {
	if len(p.segs) != len(path) {
		return false
	}
	fixed := len(p.segs)
	if p.wild {
		fixed--
	}
	for i := 0; i < fixed; i++ {
		if p.segs[i] != path[i] {
			return false
		}
	}
	return true
}

// isAncestorOf 判断一条精确模式是否为给定路径的严格祖先，
// 用于“父被拒则全部后代不可见”的覆盖关系。
func (p Pattern) isAncestorOf(path []string) bool {
	if p.wild || len(p.segs) >= len(path) {
		return false
	}
	for i, seg := range p.segs {
		if seg != path[i] {
			return false
		}
	}
	return true
}
