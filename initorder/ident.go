package initorder

// isBlankIdent 报告 s 是否为空白标识符（单独的下划线）。
// 空白标识符只能出现在变量单元左侧，每次出现都是独立变量。
func isBlankIdent(s string) bool {
	return s == "_"
}

// isValidIdent 报告 s 是否为合法标识符：非空、仅由 ASCII 字母数字与
// 下划线组成、且不以数字开头。空白标识符 "_" 本身也是合法标识符，
// 是否允许出现由调用方按上下文判定。
func isValidIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
