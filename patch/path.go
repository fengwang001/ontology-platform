package patch

import (
	"fmt"
	"strings"
)

// escapeSegment 按引用规则编码单个路径段：~ -> ~0，/ -> ~1。
func escapeSegment(seg string) string {
	seg = strings.ReplaceAll(seg, "~", "~0")
	seg = strings.ReplaceAll(seg, "/", "~1")
	return seg
}

// unescapeSegment 解码单个路径段，非法转义返回 ErrInvalidPath。
func unescapeSegment(seg string) (string, error) {
	if !strings.Contains(seg, "~") {
		return seg, nil
	}
	var b strings.Builder
	b.Grow(len(seg))
	for i := 0; i < len(seg); i++ {
		if seg[i] != '~' {
			b.WriteByte(seg[i])
			continue
		}
		if i+1 >= len(seg) {
			return "", fmt.Errorf("%w: dangling escape in segment %q", ErrInvalidPath, seg)
		}
		switch seg[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fmt.Errorf("%w: bad escape ~%c in segment %q", ErrInvalidPath, seg[i+1], seg)
		}
		i++
	}
	return b.String(), nil
}

// joinPath 把若干段编码后拼接为完整路径（根为 ""）。
func joinPath(segs ...string) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte('/')
		b.WriteString(escapeSegment(s))
	}
	return b.String()
}

// parsePath 把编码路径解码为段序列，并校验长度与深度限制。
func parsePath(path string) ([]string, error) {
	if len(path) > MaxPathLen {
		return nil, fmt.Errorf("%w: path length %d exceeds %d", ErrPatchTooLarge, len(path), MaxPathLen)
	}
	if path == "" {
		return nil, nil
	}
	if path[0] != '/' {
		return nil, fmt.Errorf("%w: path %q must start with '/'", ErrInvalidPath, path)
	}
	raw := strings.Split(path[1:], "/")
	if len(raw) > MaxPathDepth {
		return nil, fmt.Errorf("%w: path depth %d exceeds %d", ErrPatchTooLarge, len(raw), MaxPathDepth)
	}
	segs := make([]string, len(raw))
	for i, r := range raw {
		s, err := unescapeSegment(r)
		if err != nil {
			return nil, err
		}
		segs[i] = s
	}
	return segs, nil
}

// parseIndex 校验并解析数组下标段：无前导零的非负整数。
func parseIndex(seg string) (int, error) {
	if seg == "" {
		return 0, fmt.Errorf("%w: empty array index", ErrInvalidPath)
	}
	if len(seg) > 1 && seg[0] == '0' {
		return 0, fmt.Errorf("%w: array index %q has leading zero", ErrInvalidPath, seg)
	}
	n := 0
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%w: array index %q is not a non-negative integer", ErrInvalidPath, seg)
		}
		n = n*10 + int(c-'0')
		if n > 1<<30 {
			return 0, fmt.Errorf("%w: array index %q out of range", ErrInvalidPath, seg)
		}
	}
	return n, nil
}
