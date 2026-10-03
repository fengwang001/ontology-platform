// Package authz 定义操作者携带的权限位集合。
package authz

// Perm 是单个权限位。
type Perm uint8

const (
	// DeleteVersion 允许永久删除指定版本。
	DeleteVersion Perm = 1 << iota
	// BypassGovernance 允许绕过生效中的 GOVERNANCE 保留。
	BypassGovernance
	// PutRetention 允许设置/清除保留期与法律保留。
	PutRetention
)

// Set 是权限位的不可变集合（值类型）。
type Set struct {
	bits uint8
}

// New 由若干权限位构造集合。
func New(perms ...Perm) Set {
	var s Set
	for _, p := range perms {
		s.bits |= uint8(p)
	}
	return s
}

// Has 报告集合是否包含权限 p。
func (s Set) Has(p Perm) bool {
	return s.bits&uint8(p) != 0
}

// With 返回追加了权限 p 的新集合。
func (s Set) With(p Perm) Set {
	s.bits |= uint8(p)
	return s
}
