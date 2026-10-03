// Package scope 提供作用域覆盖关系与需求风险分级的纯函数。
package scope

// Covers 判断已授予作用域 g 是否覆盖需求 n：
// 当且仅当 n==g 或 n 以 g+":" 开头（覆盖只向更具体的子作用域向下）。
func Covers(g, n string) bool {
	if n == g {
		return true
	}
	return len(n) > len(g) && n[:len(g)] == g && n[len(g)] == ':'
}

// AnyGranted 判断需求 n 是否被任一已授予作用域覆盖。
func AnyGranted(granted []string, n string) bool {
	for _, g := range granted {
		if Covers(g, n) {
			return true
		}
	}
	return false
}

// FirstMissing 返回 need 中第一个未被任何 granted 覆盖的项；全部被覆盖时返回空串。
func FirstMissing(granted, need []string) string {
	for _, n := range need {
		if !AnyGranted(granted, n) {
			return n
		}
	}
	return ""
}

// lastSegment 返回 s 按 ":" 分段的最后一段。
func lastSegment(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[i+1:]
		}
	}
	return s
}

// HighRisk 判断需求列表是否为高风险：
// 任一项按 ":" 分段的最后一段等于 write 或 admin 即为高风险，否则低风险。
func HighRisk(need []string) bool {
	for _, n := range need {
		seg := lastSegment(n)
		if seg == "write" || seg == "admin" {
			return true
		}
	}
	return false
}
