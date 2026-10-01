package topicstore

import "strings"

// splitLevels 按 '/' 切分层级。空串切分为 nil；"/a" 切分为 ["", "a"]，
// "a//b" 切分为 ["a", "", "b"]，即空层级被保留。
func splitLevels(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

// validateFilter 校验过滤器：
//   - 不得为空串；
//   - "#" 只能单独出现在末层；
//   - "+" 与 "#" 不得与其他字符同层（如 "a+"）。
func validateFilter(filter string) bool {
	if filter == "" {
		return false
	}
	levels := splitLevels(filter)
	for i, level := range levels {
		switch {
		case level == "#":
			if i != len(levels)-1 {
				return false
			}
		case level == "+":
			// 单独的 "+" 合法，匹配恰好一层（含空层）。
		case strings.ContainsAny(level, "+#"):
			return false
		}
	}
	return true
}

// validateTopic 校验发布主题：不得为空串，且不得含通配字符。
func validateTopic(topic string) bool {
	if topic == "" {
		return false
	}
	if strings.ContainsAny(topic, "+#") {
		return false
	}
	return true
}

// matchLevels 是逐层朴素匹配：filter 与 topic 均为已切分的层级。
//
// 规则：
//   - 字面层级必须完全相等；
//   - "+" 匹配恰好一层（含空层）；
//   - "#" 只能在末层，匹配父层及其下任意多层（含零层），
//     即 "a/#" 匹配 "a"、"a/b"、"a/b/c"；
//   - 首层以 "$" 开头的主题不被首层为 "+" 或 "#" 的过滤器匹配，
//     但被首层写明同一字面值（含以 "$" 开头）的过滤器匹配。
func matchLevels(filter, topic []string) bool {
	if len(topic) > 0 && strings.HasPrefix(topic[0], "$") &&
		len(filter) > 0 && (filter[0] == "+" || filter[0] == "#") {
		return false
	}
	i := 0
	for ; i < len(filter); i++ {
		f := filter[i]
		if f == "#" {
			return true
		}
		if i >= len(topic) {
			return false
		}
		if f != "+" && f != topic[i] {
			return false
		}
	}
	return i == len(topic)
}

// match 是字符串版本的逐层匹配，过滤器假定已通过 validateFilter。
func match(filter, topic string) bool {
	return matchLevels(splitLevels(filter), splitLevels(topic))
}
