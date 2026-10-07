package pathlock

import "strings"

// NormalizePath 把用户输入规范化为仓库内的规范路径：
// 去掉重复分隔符、消解 "." 与 ".." 成分、去掉首尾分隔符。
// 规范化后为空、逃出仓库根（".." 多于已有成分）、或含有不可打印字节
// （< 0x20 或 0x7F）的输入返回 ErrInvalidArgument。
// 两个输入只要规范化后相同就是同一把锁。
func NormalizePath(p string) (string, error) {
	for i := 0; i < len(p); i++ {
		if b := p[i]; b < 0x20 || b == 0x7f {
			return "", invalidArg("路径含有不可打印字节")
		}
	}
	var stack []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(stack) == 0 {
				return "", invalidArg("路径逃出仓库根: " + p)
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, seg)
		}
	}
	if len(stack) == 0 {
		return "", invalidArg("路径规范化后为空")
	}
	return strings.Join(stack, "/"), nil
}
