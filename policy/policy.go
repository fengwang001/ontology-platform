// Package policy 提供紧急访问授权服务的纯函数策略原语：
// 角色、策略参数校验、资源路径校验与段边界覆盖判断。
package policy

import "strings"

const (
	MaxNameBytes = 64
	MaxPathBytes = 128
	MaxScopes    = 8
	MaxBound     = 1_000_000_000     // Dmax/Rw/Lk/Cmax 的上界（含）
	MaxClock     = 1_000_000_000_000 // now 的上界（含）
)

// Role 是主体角色。
type Role string

const (
	RoleEngineer Role = "engineer"
	RoleManager  Role = "manager"
	RoleSecurity Role = "security"
)

// ValidRole 报告 role 是否为已定义角色。
func ValidRole(role Role) bool {
	switch role {
	case RoleEngineer, RoleManager, RoleSecurity:
		return true
	}
	return false
}

// Policy 是一版授权策略。Lk 可为 0，其余字段须在 [1, MaxBound]。
type Policy struct {
	Dmax int64 // 单次授权时长上界
	Rw   int64 // 评审窗口
	Lk   int64 // 锁定时长
	Cmax int64 // 单申请人并发授权上限
}

// Valid 报告策略参数是否全部合法。
func (p Policy) Valid() bool {
	in := func(v int64) bool { return v >= 1 && v <= MaxBound }
	return in(p.Dmax) && in(p.Rw) && in(p.Cmax) && p.Lk >= 0 && p.Lk <= MaxBound
}

// ValidName 报告主体名是否非空且不超过 64 字节。
func ValidName(name string) bool {
	return len(name) >= 1 && len(name) <= MaxNameBytes
}

// ValidPath 报告路径是否形如 "/a/b"：以 / 开头、无尾部 /、
// 无空段、不超过 128 字节。
func ValidPath(path string) bool {
	if len(path) < 2 || len(path) > MaxPathBytes || path[0] != '/' {
		return false
	}
	if path[len(path)-1] == '/' {
		return false
	}
	for _, seg := range strings.Split(path[1:], "/") {
		if seg == "" {
			return false
		}
	}
	return true
}

// ValidScopes 报告角色 role 的作用域列表是否合法：
// manager 须带 1 到 8 个合法路径，其余角色须为空。
func ValidScopes(role Role, scopes []string) bool {
	if role == RoleManager {
		if len(scopes) < 1 || len(scopes) > MaxScopes {
			return false
		}
		for _, s := range scopes {
			if !ValidPath(s) {
				return false
			}
		}
		return true
	}
	return len(scopes) == 0
}

// Covers 报告 a 是否按段边界覆盖 b：b 等于 a，或 b 以 a+"/" 开头。
// 因此 "/d" 不覆盖 "/db/prod"。资源与作用域共用此判断。
func Covers(a, b string) bool {
	return b == a || strings.HasPrefix(b, a+"/")
}

// ValidNow 报告时刻是否在 [0, MaxClock]。
func ValidNow(now int64) bool {
	return now >= 0 && now <= MaxClock
}
