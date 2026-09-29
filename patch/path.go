package patch

import (
	"fmt"
	"strconv"
	"strings"
)

// EncodePath 把已解码的路径段序列化为指针路径。
// 转义顺序固定：先把 "~" 写成 "~1"，再把 "/" 写成 "~0"，各段之间以 "/" 连接。
// 空段不允许出现（它在解码端无法与空路径区分），返回 ErrInvalidPath。
func EncodePath(segments []string) (string, error) {
	var b strings.Builder
	for _, seg := range segments {
		if seg == "" {
			return "", fmt.Errorf("%w: empty segment", ErrInvalidPath)
		}
		b.WriteByte('/')
		for i := 0; i < len(seg); i++ {
			switch seg[i] {
			case '~':
				b.WriteString("~1")
			case '/':
				b.WriteString("~0")
			default:
				b.WriteByte(seg[i])
			}
		}
	}
	return b.String(), nil
}

// DecodePath 解析指针路径为原始键段。
// 空字符串表示根路径（零个段）；非根路径必须以 "/" 开头。
// 非法转义（"~" 后不是 0/1）、空段均返回 ErrInvalidPath。
func DecodePath(path string) ([]string, error) {
	if path == "" {
		return []string{}, nil
	}
	if path[0] != '/' {
		return nil, fmt.Errorf("%w: path must start with '/' or be empty", ErrInvalidPath)
	}
	raw := strings.Split(path[1:], "/")
	segments := make([]string, 0, len(raw))
	for _, part := range raw {
		if part == "" {
			return nil, fmt.Errorf("%w: empty segment in %q", ErrInvalidPath, path)
		}
		seg, err := decodeSegment(part)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %v", ErrInvalidPath, path, err)
		}
		segments = append(segments, seg)
	}
	return segments, nil
}

func decodeSegment(part string) (string, error) {
	if !strings.ContainsRune(part, '~') {
		return part, nil
	}
	var b strings.Builder
	b.Grow(len(part))
	for i := 0; i < len(part); i++ {
		if part[i] != '~' {
			b.WriteByte(part[i])
			continue
		}
		if i+1 >= len(part) {
			return "", fmt.Errorf("dangling escape")
		}
		switch part[i+1] {
		case '0':
			b.WriteByte('/')
		case '1':
			b.WriteByte('~')
		default:
			return "", fmt.Errorf("invalid escape '~%c'", part[i+1])
		}
		i++
	}
	return b.String(), nil
}

// parseArrayIndex 把路径段解析为数组下标：必须是非负整数、十进制、无前导零。
func parseArrayIndex(seg string) (int, error) {
	if seg == "" {
		return 0, fmt.Errorf("empty index")
	}
	if len(seg) > 1 && seg[0] == '0' {
		return 0, fmt.Errorf("leading zero in index %q", seg)
	}
	if seg[0] < '0' || seg[0] > '9' {
		return 0, fmt.Errorf("index %q is not a non-negative integer", seg)
	}
	for i := 1; i < len(seg); i++ {
		if seg[i] < '0' || seg[i] > '9' {
			return 0, fmt.Errorf("index %q is not a non-negative integer", seg)
		}
	}
	idx, err := strconv.Atoi(seg)
	if err != nil {
		return 0, fmt.Errorf("index %q out of range: %w", seg, err)
	}
	return idx, nil
}

// parentAndToken 拆分出父路径与最后一个段（已解码）。
func parentAndToken(segments []string) (parent []string, token string) {
	if len(segments) == 0 {
		return nil, ""
	}
	return segments[:len(segments)-1], segments[len(segments)-1]
}
