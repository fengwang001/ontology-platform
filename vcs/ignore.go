package vcs

import (
	"path"
	"strings"
)

// IgnoreSet 简化版 gitignore 规则集。只影响未跟踪路径的分类。
// 规则：空行与 # 开头忽略；"dir/" 形式匹配任意层级同名目录下的内容；
// 含 "/" 的模式从根整体 glob；否则匹配任意路径分量（path.Match 语法）。
type IgnoreSet struct {
	patterns []string
}

func NewIgnoreSet(patterns ...string) *IgnoreSet {
	cp := make([]string, len(patterns))
	copy(cp, patterns)
	return &IgnoreSet{patterns: cp}
}

// Patterns 返回规则副本。
func (s *IgnoreSet) Patterns() []string {
	if s == nil {
		return nil
	}
	cp := make([]string, len(s.patterns))
	copy(cp, s.patterns)
	return cp
}

// Match 判断已规范化路径 p 是否命中任一规则。
func (s *IgnoreSet) Match(p string) bool {
	if s == nil {
		return false
	}
	for _, pat := range s.patterns {
		if matchPattern(pat, p) {
			return true
		}
	}
	return false
}

func matchPattern(pat, p string) bool {
	pat = strings.TrimSpace(pat)
	if pat == "" || strings.HasPrefix(pat, "#") {
		return false
	}
	dirOnly := strings.HasSuffix(pat, "/")
	pat = strings.TrimSuffix(pat, "/")
	if pat == "" {
		return false
	}
	comps := strings.Split(p, "/")
	if strings.Contains(pat, "/") {
		if !dirOnly {
			if ok, _ := path.Match(pat, p); ok {
				return true
			}
		}
		for i := 1; i < len(comps); i++ {
			if ok, _ := path.Match(pat, strings.Join(comps[:i], "/")); ok {
				return true
			}
		}
		return false
	}
	limit := len(comps)
	if dirOnly {
		limit = len(comps) - 1 // 目录模式只匹配祖先分量
	}
	for i := 0; i < limit; i++ {
		if ok, _ := path.Match(pat, comps[i]); ok {
			return true
		}
	}
	return false
}
