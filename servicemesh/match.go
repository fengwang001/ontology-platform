package servicemesh

import "strings"

// stripQuery 去掉查询串（首个 '?' 及其后内容）。
func stripQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

// matchPath 判断请求路径是否满足路径条件（段边界前缀）。
func matchPath(cond PathMatch, requestPath string) bool {
	if cond.Path == "" || cond.Path[0] != '/' {
		return false
	}
	if cond.Kind == PathExact {
		return requestPath == cond.Path
	}
	// 段边界：请求路径等于前缀，或前缀后紧跟 '/'。
	if requestPath == cond.Path {
		return true
	}
	return strings.HasPrefix(requestPath, cond.Path) &&
		strings.HasPrefix(requestPath[len(cond.Path):], "/")
}

// headerName 归一化头名（不区分大小写）。
func headerName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// matchHeader 判断一组请求头值是否满足单个头条件（多值时任一值满足）。
func matchHeader(cond HeaderMatch, values []string) bool {
	for _, v := range values {
		switch cond.Op {
		case HeaderPresent:
			return true
		case HeaderExact:
			if v == cond.Value {
				return true
			}
		case HeaderPrefix:
			if strings.HasPrefix(v, cond.Value) {
				return true
			}
		}
	}
	return false
}

// pathCovers 判断路径条件 a 是否覆盖 b（a 能匹配的请求集合包含 b 的）。
func pathCovers(a, b PathMatch) bool {
	if a.Kind == PathExact {
		return b.Kind == PathExact && a.Path == b.Path
	}
	// a 是前缀：b 精确等于 a 或以 a 为段边界前缀时被覆盖。
	if b.Kind == PathExact {
		return matchPath(a, b.Path)
	}
	if a.Path == b.Path {
		return true
	}
	return matchPath(a, b.Path)
}

// headerEntails 判断头条件 a 是否蕴含 b（同名前提下）。
func headerEntails(a, b HeaderMatch) bool {
	switch a.Op {
	case HeaderExact:
		// 精确值蕴含同值精确、以其开头的前缀、存在。
		switch b.Op {
		case HeaderExact:
			return a.Value == b.Value
		case HeaderPrefix:
			return strings.HasPrefix(a.Value, b.Value)
		case HeaderPresent:
			return true
		}
	case HeaderPrefix:
		// 前缀蕴含更短/等长前缀与存在（同值精确时空串等特例由调用方语义排除）。
		switch b.Op {
		case HeaderPrefix:
			return strings.HasPrefix(a.Value, b.Value)
		case HeaderPresent:
			return true
		}
	case HeaderPresent:
		return b.Op == HeaderPresent
	}
	return false
}

// itemCovers 判断匹配项 a 是否完全覆盖匹配项 b。
func itemCovers(a, b MatchItem) bool {
	if !pathCovers(a.Path, b.Path) {
		return false
	}
	// a 的每一个头条件都必须被 b 的某个同名头条件蕴含。
	for _, ah := range a.Headers {
		entailed := false
		for _, bh := range b.Headers {
			if headerName(ah.Name) == headerName(bh.Name) && headerEntails(ah, bh) {
				entailed = true
				break
			}
		}
		if !entailed {
			return false
		}
	}
	return true
}
