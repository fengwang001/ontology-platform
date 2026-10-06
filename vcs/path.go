package vcs

import "strings"

// Normalize 规范化路径：以 "/" 分段，丢弃空段与 "."，拒绝 ".."（逃出根目录）。
// 规范化后为空视为参数非法。
func Normalize(p string) (string, error) {
	if p == "" {
		return "", errf(CodeInvalidPath, p, "路径为空")
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, c := range parts {
		switch c {
		case "", ".":
			continue
		case "..":
			return "", errf(CodeInvalidPath, p, "路径逃出根目录")
		default:
			if strings.ContainsRune(c, '\x00') {
				return "", errf(CodeInvalidPath, p, "路径含非法字符")
			}
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "", errf(CodeInvalidPath, p, "规范化后为空")
	}
	return strings.Join(out, "/"), nil
}

// prefixes 返回 p 的全部真祖先前缀，如 "a/b/c" -> ["a", "a/b"]。
func prefixes(p string) []string {
	parts := strings.Split(p, "/")
	res := make([]string, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		res = append(res, strings.Join(parts[:i], "/"))
	}
	return res
}

// pathSet 维护工作区全部已存在路径（三份内容与冲突表的并集），
// 用目录前缀引用计数支持 O(路径深度) 的祖先/后代冲突检查。
type pathSet struct {
	files map[string]struct{}
	dirs  map[string]int // 目录前缀 -> 其下文件数
}

func newPathSet() *pathSet {
	return &pathSet{files: map[string]struct{}{}, dirs: map[string]int{}}
}

func (s *pathSet) has(p string) bool {
	_, ok := s.files[p]
	return ok
}

// compatible 检查 p 是否与已存在路径兼容：p 不是目录前缀，且 p 的祖先不是文件。
func (s *pathSet) compatible(p string) bool {
	if s.dirs[p] > 0 {
		return false
	}
	for _, pre := range prefixes(p) {
		if s.has(pre) {
			return false
		}
	}
	return true
}

func (s *pathSet) add(p string) {
	if s.has(p) {
		return
	}
	s.files[p] = struct{}{}
	for _, pre := range prefixes(p) {
		s.dirs[pre]++
	}
}

func (s *pathSet) remove(p string) {
	if !s.has(p) {
		return
	}
	delete(s.files, p)
	for _, pre := range prefixes(p) {
		s.dirs[pre]--
		if s.dirs[pre] <= 0 {
			delete(s.dirs, pre)
		}
	}
}
