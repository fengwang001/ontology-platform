package scope

// Visibility 是组织级密钥对仓库的可见性策略。
type Visibility int

const (
	All Visibility = iota
	Private
	Selected
)

// Match 判定 ref 是否匹配模式：末尾 '*' 为前缀匹配，"*" 匹配一切，否则精确相等。
func Match(pattern, ref string) bool {
	if pattern == "" {
		return false
	}
	if pattern[len(pattern)-1] != '*' {
		return pattern == ref
	}
	prefix := pattern[:len(pattern)-1]
	return len(ref) >= len(prefix) && ref[:len(prefix)] == prefix
}

// ValidPatterns 校验模式列表（原地去重不做，只判合法性）。
func ValidPatterns(patterns []string) bool {
	for _, p := range patterns {
		if !validPattern(p) {
			return false
		}
	}
	return true
}

func validPattern(p string) bool {
	if p == "" {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '*' {
			return i == len(p)-1
		}
	}
	return true
}

// ValidName 校验密钥名：[A-Z_][A-Z0-9_]* 且不超过 64 字节。
func ValidName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	if !isNameStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isNamePart(name[i]) {
			return false
		}
	}
	return true
}

func isNameStart(c byte) bool {
	return c == '_' || c >= 'A' && c <= 'Z'
}

func isNamePart(c byte) bool {
	return isNameStart(c) || c >= '0' && c <= '9'
}

// ValidValue 校验值长度：1..4096 字节。
func ValidValue(value string) bool {
	return len(value) >= 1 && len(value) <= 4096
}

// Protected 判定分支是否受保护：仅 push 事件且匹配任一仓库保护模式。
func Protected(event, ref string, protectedPatterns []string) bool {
	if event != "push" {
		return false
	}
	for _, p := range protectedPatterns {
		if Match(p, ref) {
			return true
		}
	}
	return false
}

// Visible 判定组织级定义对某仓库是否可见。
func Visible(vis Visibility, private bool, selectedRepos []string, repo string) bool {
	switch vis {
	case All:
		return true
	case Private:
		return private
	case Selected:
		for _, r := range selectedRepos {
			if r == repo {
				return true
			}
		}
		return false
	default:
		return false
	}
}
