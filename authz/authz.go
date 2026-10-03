package authz

import (
	"sort"
	"strings"
)

// Normalize 归一化授权前缀：去重并去掉被更短前缀覆盖的前缀，结果按字节序排列。
// 含空串时结果恒为 [""]；空入参表示一个都看不见，返回 nil。
func Normalize(grants []string) []string {
	if len(grants) == 0 {
		return nil
	}
	sorted := append([]string(nil), grants...)
	sort.Strings(sorted)

	uniq := sorted[:0]
	for _, g := range sorted {
		if len(uniq) > 0 && uniq[len(uniq)-1] == g {
			continue
		}
		uniq = append(uniq, g)
	}
	if len(uniq) > 0 && uniq[0] == "" {
		return []string{""}
	}

	out := make([]string, 0, len(uniq))
	for _, g := range uniq {
		covered := false
		for _, kept := range out {
			if strings.HasPrefix(g, kept) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, g)
		}
	}
	return out
}
