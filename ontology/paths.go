package ontology

import "strings"

// validateOpPath checks an operation path. Operation paths never carry a
// trailing slash. The empty path (root) is allowed only when allowRoot.
func validateOpPath(path string, allowRoot bool) bool {
	if path == "" {
		return allowRoot
	}
	if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// parent returns the parent path ("" for a top-level name).
func parent(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return ""
	}
	return path[:i]
}

// ancestors returns the proper ancestors of path in root-down order,
// starting with the root ("").
func ancestors(path string) []string {
	segs := strings.Split(path, "/")
	out := make([]string, 0, len(segs))
	out = append(out, "")
	cur := ""
	for i := 0; i+1 < len(segs); i++ {
		if cur == "" {
			cur = segs[i]
		} else {
			cur = cur + "/" + segs[i]
		}
		out = append(out, cur)
	}
	return out
}

// isUnder reports whether p equals base or lies below base.
func isUnder(p, base string) bool {
	if p == base {
		return true
	}
	return strings.HasPrefix(p, base+"/")
}
