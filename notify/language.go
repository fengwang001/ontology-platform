package notify

import "strings"

// validateLanguage 按语言标签规则校验：
// 第一个子标签 2-3 个小写字母；
// 第二个为 4 个首字母大写的字母（文字）或 2 个大写字母（地区）；
// 第三个只允许跟在文字之后，为 2 个大写字母。
func validateLanguage(loc string) bool {
	subs := strings.Split(loc, "-")
	if len(subs) < 1 || len(subs) > 3 {
		return false
	}
	first := subs[0]
	if len(first) < 2 || len(first) > 3 || !allLowerAlpha(first) {
		return false
	}
	if len(subs) == 1 {
		return true
	}
	second := subs[1]
	if len(second) == 2 {
		if !allUpperAlpha(second) {
			return false
		}
		return len(subs) == 2
	}
	if len(second) == 4 {
		if !isScriptTag(second) {
			return false
		}
		if len(subs) == 2 {
			return true
		}
		return allUpperAlpha(subs[2]) && len(subs[2]) == 2
	}
	return false
}

func allLowerAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

func allUpperAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func isScriptTag(s string) bool {
	if len(s) != 4 {
		return false
	}
	if s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for i := 1; i < 4; i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

// fallbackChain 构造回退链：语言自身，逐段去掉最后一个子标签，
// 最后追加默认语言（已在链中则不重复），保持顺序。
func fallbackChain(loc, defaultLang string) []string {
	subs := strings.Split(loc, "-")
	chain := make([]string, 0, len(subs)+1)
	for n := len(subs); n >= 1; n-- {
		chain = append(chain, strings.Join(subs[:n], "-"))
	}
	for _, l := range chain {
		if l == defaultLang {
			return chain
		}
	}
	chain = append(chain, defaultLang)
	return chain
}
