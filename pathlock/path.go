package pathlock

import "strings"

// Normalize 把输入路径规范化为仓库内的规范化路径：
// 去掉重复分隔符、消解 "." 与 ".." 成分、去掉首尾分隔符。
// 返回 ErrInvalidPath 当且仅当：含不可打印字节、逃出仓库根、或规范化后为空。
func Normalize(p string) (string, error) {
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c < 0x20 || c == 0x7f {
			return "", ErrInvalidPath
		}
	}
	parts := strings.Split(p, "/")
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return "", ErrInvalidPath
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}
	if len(stack) == 0 {
		return "", ErrInvalidPath
	}
	return strings.Join(stack, "/"), nil
}
