// Package authz 负责授权前缀的归一化与可见性判定。
package authz

import "sort"

// Grants 是归一化后的授权前缀列表：升序、去重、互不覆盖。
type Grants []string

// Normalize 去重并丢弃被更短前缀覆盖的授权前缀；nil 表示一个都看不见。
// 含空串前缀即全部可见；空入参（一个授权都没有）归一化为 nil。
func Normalize(in []string) Grants {
	if len(in) == 0 {
		return nil
	}
	gs := append([]string(nil), in...)
	sort.Strings(gs)

	out := make(Grants, 0, len(gs))
	for _, g := range gs {
		if len(out) > 0 {
			if out[len(out)-1] == g {
				continue // 去重
			}
			if last := out[len(out)-1]; g != last && hasPrefix(g, last) {
				continue // g 被更短的 last 覆盖
			}
		}
		out = append(out, g)
	}
	return out
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// Visible 判断键是否以某个授权前缀开头。
func (g Grants) Visible(key string) bool {
	n := sort.Search(len(g), func(i int) bool { return g[i] >= key })
	if n < len(g) && g[n] == key {
		return true
	}
	return n > 0 && hasPrefix(key, g[n-1])
}

// NextVisible 返回不小于 key 且可能命中某个授权前缀的最小键；
// ok 为 false 表示授权键空间已结束。
func (g Grants) NextVisible(key string) (string, bool) {
	for _, p := range g {
		if hasPrefix(key, p) {
			return key, true // 已落在授权区间内
		}
		if p > key {
			return p, true // 跳到下一授权前缀起点
		}
	}
	return "", false
}
