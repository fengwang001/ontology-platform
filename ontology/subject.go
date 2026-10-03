package ontology

// normalizeSubject 反复去掉开头的空白与回复/转发前缀，最后去掉首尾空白。
// 规则：
//   - 前缀（与冒号之间不许有空白）：re:、fw:、fwd:（不分大小写），
//     以及「回复:」「回复：」「转发:」「转发：」（原样匹配）。
//   - 每轮先去掉前导空白，若当前位置是某个前缀则截掉后继续，否则结束。
//   - 结束后再 TrimSpace；其余字节原样保留（区分大小写比较）。
//
// 返回规范化主题与是否至少去掉过一个前缀（且最终结果非空才是回复主题，
// 由调用方结合返回的字符串判断）。
func normalizeSubject(s string) (string, bool) {
	stripped := false
	for {
		start := 0
		for start < len(s) && isASCIISpace(s[start]) {
			start++
		}
		s = s[start:]
		if s == "" {
			return "", stripped
		}
		next, ok := trimPrefixFold(s)
		if !ok {
			return trimRightSpace(s), stripped
		}
		s = next
		stripped = true
	}
}

// trimPrefixFold 尝试在字符串开头匹配一个回复/转发前缀，返回截掉前缀后的剩余串。
func trimPrefixFold(s string) (string, bool) {
	// ASCII 前缀（不分大小写）：re:、fw:、fwd:。按长前缀优先匹配，
	// 避免 fwd: 被 fw: 规则干扰（两者都以 fw 开头，但后者要求紧跟冒号）。
	ascii := []string{"fwd:", "fw:", "re:"}
	for _, p := range ascii {
		if len(s) >= len(p) && equalFoldASCII(s[:len(p)], p) {
			return s[len(p):], true
		}
	}
	// 中文前缀（区分原样匹配，冒号可为半角/全角）。
	cjk := []string{"回复:", "回复：", "转发:", "转发："}
	for _, p := range cjk {
		if len(s) >= len(p) && s[:len(p)] == p {
			return s[len(p):], true
		}
	}
	return s, false
}

// equalFoldASCII 判断两个等长 ASCII 串是否在忽略 ASCII 大小写意义下相同。
func equalFoldASCII(a, b string) bool {
	for i := 0; i < len(a); i++ {
		if toLowerASCII(a[i]) != toLowerASCII(b[i]) {
			return false
		}
	}
	return true
}

func toLowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func isASCIISpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

func trimRightSpace(s string) string {
	end := len(s)
	for end > 0 && isASCIISpace(s[end-1]) {
		end--
	}
	return s[:end]
}
