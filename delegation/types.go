// Package delegation 实现本体平台的角色委托链模块。
//
// 角色持有者（主体）可以把其在对象类型上的属性级与行级权限的子集，
// 在指定有效期内委托给其他主体，并标记是否允许再委托。委托支持：
// 权限收缩级联、多路径独立有效、成环检测、历史判定不可追溯、
// 并发线性化以及可观测的遍历开销上界。
package delegation

import (
	"errors"
	"fmt"
	"sort"
)

// Permission 表示某对象类型上的一条属性级 + 行级权限。
// Attribute 为属性标识（"*" 表示全部属性），RowScope 为行级范围标识
// （"*" 表示全部行，否则为行过滤规则标识）。
type Permission struct {
	ObjectType string
	Attribute  string
	RowScope   string
}

func (p Permission) String() string {
	return fmt.Sprintf("%s.%s@%s", p.ObjectType, p.Attribute, p.RowScope)
}

// PermissionSet 是权限的集合（去重）。
type PermissionSet map[Permission]struct{}

// NewPermissionSet 由权限列表构造集合。
func NewPermissionSet(perms ...Permission) PermissionSet {
	s := make(PermissionSet, len(perms))
	for _, p := range perms {
		s[p] = struct{}{}
	}
	return s
}

// Contains 判断集合是否包含某权限。
func (s PermissionSet) Contains(p Permission) bool {
	_, ok := s[p]
	return ok
}

// SubsetOf 判断 s 是否为 other 的子集。
func (s PermissionSet) SubsetOf(other PermissionSet) bool {
	for p := range s {
		if !other.Contains(p) {
			return false
		}
	}
	return true
}

// Clone 返回集合副本。
func (s PermissionSet) Clone() PermissionSet {
	c := make(PermissionSet, len(s))
	for p := range s {
		c[p] = struct{}{}
	}
	return c
}

// Union 把 other 的内容并入 s。
func (s PermissionSet) Union(other PermissionSet) {
	for p := range other {
		s[p] = struct{}{}
	}
}

// Sorted 返回确定性排序的权限列表，用于日志与测试比对。
func (s PermissionSet) Sorted() []Permission {
	out := make([]Permission, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ObjectType != out[j].ObjectType {
			return out[i].ObjectType < out[j].ObjectType
		}
		if out[i].Attribute != out[j].Attribute {
			return out[i].Attribute < out[j].Attribute
		}
		return out[i].RowScope < out[j].RowScope
	})
	return out
}

// 四类语义错误，Declare 按此固定优先顺序汇报：
//  1. ErrScopeExceeded      声明子集超出委托方当前实际拥有范围
//  2. ErrRedelegationDenied 依赖的上游委托不允许再委托
//  3. ErrCycle              新增委托将导致委托链成环
//  4. ErrExpired            委托已超出有效期
var (
	ErrScopeExceeded      = errors.New("delegation: declared subset exceeds delegator's effective permissions")
	ErrRedelegationDenied = errors.New("delegation: upstream delegation forbids redelegation")
	ErrCycle              = errors.New("delegation: delegation would create a cycle")
	ErrExpired            = errors.New("delegation: validity period has already elapsed")
	ErrInvalidArgument    = errors.New("delegation: invalid argument")
	ErrDelegationNotFound = errors.New("delegation: delegation not found")
)
