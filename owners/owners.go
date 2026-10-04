package owners

// Rule 是一条有序的属主规则：Pattern 决定匹配方式，Owners 为属主列表（可空）。
type Rule struct {
	Pattern string
	Owners  []string
}

// Rules 是不可变的有序规则集合，属主匹配遵循后者优先。
type Rules struct {
	rules []Rule
}

// New 防御性拷贝规则后返回规则集合。
func New(rules []Rule) *Rules {
	copied := make([]Rule, len(rules))
	for i, rule := range rules {
		copied[i] = Rule{Pattern: rule.Pattern, Owners: append([]string(nil), rule.Owners...)}
	}
	return &Rules{rules: copied}
}

// Owners 返回 path 的属主列表：取最后一条匹配规则的拷贝；
// 匹配空属主规则或无任何规则匹配时返回 nil（无属主）。
func (r *Rules) Owners(path string) []string {
	var found []string
	matched := false
	for _, rule := range r.rules {
		if !match(rule.Pattern, path) {
			continue
		}
		matched = true
		found = rule.Owners
	}
	if !matched || len(found) == 0 {
		return nil
	}
	return append([]string(nil), found...)
}

// match 实现四种模式语义：
// "*" 匹配一切；以 "/" 结尾为前缀匹配；以 "*." 开头为任意目录后缀匹配；其余精确相等。
func match(pattern, path string) bool {
	switch {
	case pattern == "*":
		return true
	case len(pattern) > 0 && pattern[len(pattern)-1] == '/':
		return len(path) >= len(pattern) && path[:len(pattern)] == pattern
	case len(pattern) >= 2 && pattern[0] == '*' && pattern[1] == '.':
		suffix := pattern[1:] // ".go"
		return len(path) > len(suffix) && path[len(path)-len(suffix):] == suffix
	default:
		return pattern == path
	}
}
