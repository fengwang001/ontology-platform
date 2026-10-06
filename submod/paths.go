package submod

import (
	"fmt"
	"path"
	"strings"
)

// NormalizePath 规范化挂载路径：去空白、合并多余分隔符、解析 "." 与 ".."、
// 去掉前导 "/"。空路径（含规范化后为空）报 ErrInvalidParam。
func NormalizePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("%w: empty mount path", ErrInvalidParam)
	}
	cleaned := path.Clean("/" + p)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("%w: empty mount path %q", ErrInvalidParam, p)
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: path %q escapes root", ErrInvalidParam, p)
		}
	}
	return cleaned, nil
}

// isAncestor 报告 a 是否是 b 的祖先目录（严格前缀，按段对齐）。
func isAncestor(a, b string) bool {
	return strings.HasPrefix(b, a+"/")
}

// pathsConflict 报告两个已规范化路径是否冲突：相同或互为祖先后代。
func pathsConflict(a, b string) bool {
	return a == b || isAncestor(a, b) || isAncestor(b, a)
}

// joinPath 拼接挂载全路径。
func joinPath(parent, rel string) string {
	if parent == "" {
		return rel
	}
	return parent + "/" + rel
}
