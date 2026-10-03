// Package policy 定义紧急访问策略、角色与路径/作用域规则。
package policy

import "strings"

// MaxParam 是策略字段允许的最大值。
const MaxParam = 1_000_000_000

// MaxTime 是所有 now 允许的最大值。
const MaxTime = 1_000_000_000_000

// Policy 描述某一版本的紧急访问策略。
type Policy struct {
	Dmax int64 // 授权时长上限
	Rw   int64 // 评审窗口
	Lk   int64 // 锁定时长，可为 0
	Cmax int64 // 单主体并发授权上限
}

// Valid 报告策略字段是否都在允许范围内。
func Valid(p Policy) bool {
	in := func(v int64) bool { return v >= 1 && v <= MaxParam }
	return in(p.Dmax) && in(p.Rw) && in(p.Cmax) && p.Lk >= 0 && p.Lk <= MaxParam
}

// Role 是主体角色。
type Role string

const (
	Engineer Role = "engineer"
	Manager  Role = "manager"
	Security Role = "security"
)

// ValidRole 报告角色是否合法。
func ValidRole(r Role) bool {
	return r == Engineer || r == Manager || r == Security
}

// ValidPath 报告路径是否形如 "/a/b"。
func ValidPath(s string) bool {
	if len(s) == 0 || len(s) > 128 || s[0] != '/' {
		return false
	}
	if len(s) > 1 && s[len(s)-1] == '/' {
		return false
	}
	for _, seg := range strings.Split(s[1:], "/") {
		if seg == "" {
			return false
		}
	}
	return true
}

// Covers 报告 a 是否按段边界覆盖 b。
func Covers(a, b string) bool {
	return b == a || strings.HasPrefix(b, a+"/")
}
