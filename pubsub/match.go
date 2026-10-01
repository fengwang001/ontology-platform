package pubsub

// splitLevels 按 '/' 切分层级，保留空层。
// "/a" => ["", "a"]；"a//b" => ["a", "", "b"]；"" => [""]（空层级由校验拒绝）。
func splitLevels(s string) []string {
	levels := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			levels = append(levels, s[start:i])
			start = i + 1
		}
	}
	return append(levels, s[start:])
}

// validateFilter 校验过滤器并返回其层级。
// 非法情形：空串；某层为 "#" 但不是末层；层内 '+' 或 '#' 与其他字符共存。
// 单独的 "#" 合法。
func validateFilter(filter string) ([]string, error) {
	if filter == "" {
		return nil, ErrInvalidFilter
	}
	levels := splitLevels(filter)
	for i, level := range levels {
		switch {
		case level == "#":
			if i != len(levels)-1 {
				return nil, ErrInvalidFilter
			}
		case level == "+":
			// 单层 "+" 始终合法。
		case containsAny(level, "#+"):
			// 通配符与其他字符同层（如 "a+"、"#b"、"+x"）非法。
			return nil, ErrInvalidFilter
		}
	}
	return levels, nil
}

// validateTopic 校验发布主题并返回其层级。
// 非法情形：空串；任一层包含 '+' 或 '#'。
func validateTopic(topic string) ([]string, error) {
	if topic == "" {
		return nil, ErrInvalidTopic
	}
	levels := splitLevels(topic)
	for _, level := range levels {
		if containsAny(level, "#+") {
			return nil, ErrInvalidTopic
		}
	}
	return levels, nil
}

func containsAny(s, chars string) bool {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return true
			}
		}
	}
	return false
}

// naiveMatch 是逐层朴素匹配，作为权威参考实现。
// 返回该过滤器是否命中主题。topicLevels 假定已通过 validateTopic。
//
// 规则：
//   - 末层为 "#" 时匹配其父层前缀（含零层），即主题前 len-1 层必须逐字相等；
//   - 其余层 "+" 匹配恰好一层（含空层）；
//   - 其余层需逐字相等；
//   - 主题首层以 '$' 开头时，过滤器首层为 "+" 或 "#" 不匹配，
//     过滤器首层写明同一字面值仍可匹配。
func naiveMatch(filterLevels, topicLevels []string) bool {
	if len(topicLevels) > 0 && len(topicLevels[0]) > 0 && topicLevels[0][0] == '$' {
		if len(filterLevels) > 0 && (filterLevels[0] == "+" || filterLevels[0] == "#") {
			return false
		}
	}

	last := len(filterLevels) - 1
	if filterLevels[last] == "#" {
		parentDepth := last // "#" 之前的层数
		if len(topicLevels) < parentDepth {
			return false
		}
		for i := 0; i < parentDepth; i++ {
			if !levelEqual(filterLevels[i], topicLevels[i]) {
				return false
			}
		}
		return true
	}

	if len(filterLevels) != len(topicLevels) {
		return false
	}
	for i := range filterLevels {
		if !levelEqual(filterLevels[i], topicLevels[i]) {
			return false
		}
	}
	return true
}

// levelEqual 判定单层是否相等：'+' 匹配任意单层（含空层），其余逐字比较。
func levelEqual(filterLevel, topicLevel string) bool {
	return filterLevel == "+" || filterLevel == topicLevel
}
