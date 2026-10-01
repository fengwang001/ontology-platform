package depsolver

import "strings"

type comparator struct {
	op       string
	version  Version
	upper    Version
	upperSet bool
}

// Range 是若干比较子的交集。
type Range struct {
	text        string
	comparators []comparator
}

// ParseRange 按语法严格解析范围字符串。
func ParseRange(s string) (Range, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Range{}, ErrInvalidRange
	}
	cs := make([]comparator, 0, len(fields))
	for _, token := range fields {
		c, err := parseComparator(token)
		if err != nil {
			return Range{}, err
		}
		cs = append(cs, c)
	}
	return Range{text: s, comparators: cs}, nil
}

// String 返回范围的原始字符串。
func (r Range) String() string { return r.text }

// contains 判定候选版本是否落在范围内（含预发布规则）。
func (r Range) contains(candidate Version) bool {
	if candidate.hasPrerelease() {
		allowed := false
		for _, c := range r.comparators {
			if c.version.hasPrerelease() && c.version.coreEquals(candidate) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	for _, c := range r.comparators {
		if !c.matches(candidate) {
			return false
		}
	}
	return true
}

func parseComparator(token string) (comparator, error) {
	op := longestMatchingOp(token)
	if op == "" {
		return comparator{}, ErrInvalidRange
	}
	rest := token[len(op):]
	if rest == "" {
		return comparator{}, ErrInvalidRange
	}
	v, err := ParseVersion(rest)
	if err != nil {
		return comparator{}, ErrInvalidRange
	}
	c := comparator{op: op, version: v}
	switch op {
	case "^":
		c.upperSet = true
		switch {
		case v.major > 0:
			c.upper = mustParseVersion(v.major+1, 0, 0, nil)
		case v.minor > 0:
			c.upper = mustParseVersion(0, v.minor+1, 0, nil)
		default:
			c.upper = mustParseVersion(0, 0, v.patch+1, nil)
		}
	case "~":
		c.upperSet = true
		c.upper = mustParseVersion(v.major, v.minor+1, 0, nil)
	}
	return c, nil
}

func longestMatchingOp(token string) string {
	for _, op := range []string{">=", "<=", "=", ">", "<", "^", "~"} {
		if strings.HasPrefix(token, op) {
			return op
		}
	}
	return ""
}

func mustParseVersion(major, minor, patch int, pre []prereleaseID) Version {
	return Version{major: major, minor: minor, patch: patch, prerelease: pre}
}

func (c comparator) matches(v Version) bool {
	cmp := CompareVersion(v, c.version)
	switch c.op {
	case "=":
		return cmp == 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case "^", "~":
		return cmp >= 0 && CompareVersion(v, c.upper) < 0
	default:
		return false
	}
}
