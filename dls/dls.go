package dls

import (
	"errors"
	"fmt"
	"ontology/role"
)

// ErrForbidden 表示无权（索引不存在与无匹配条目统一报此错误，
// 调用方不得据此区分两种情形）。
var ErrForbidden = errors.New("forbidden")

// View 是某用户对某索引解析出的裁剪视图。
//   - DocsOpen 为真时文档侧无限制（M 中存在空 filter）；
//     否则文档必须使 Filters 中任一表达式为真。
//   - FieldsOpen 为真时字段侧无限制；
//     否则仅当字段名在 Fields 集合中才可见。
//
// 文档侧与字段侧各自独立合并、互不绑定。
type View struct {
	matched    []role.Entry
	Filters    []*role.Expr
	fieldAuths []role.FieldAuth
	DocsOpen   bool
	FieldsOpen bool

	// touched 是本次权限判定触碰的授权条目数（仅遍历该用户自己的角色），
	// 非导出，仅供包内测试证明其与系统中其他角色数量无关。
	touched int
}

// DocVisible 在完整文档字段上映射文档侧规则。filter 总在完整文档上求值。
func (v *View) DocVisible(fields map[string]role.Value) bool {
	if v.DocsOpen {
		return true
	}
	for _, f := range v.Filters {
		if role.Eval(*f, fields) {
			return true
		}
	}
	return false
}

// FieldVisible 映射字段侧规则。
func (v *View) FieldVisible(name string) bool {
	if v.FieldsOpen {
		return true
	}
	for _, fa := range v.fieldAuths {
		if fa.AllowsField(name) {
			return true
		}
	}
	return false
}

// MatchedEntries 暴露匹配条目数，供判定依据日志使用。
func (v *View) MatchedEntries() int { return len(v.matched) }

// Resolver 基于 role.Registry 解析用户视图。
type Resolver struct {
	reg *role.Registry
}

func NewResolver(reg *role.Registry) *Resolver {
	return &Resolver{reg: reg}
}

// Resolve 返回用户在索引 index 上的视图。
// 参数 index 仅做名字合法性校验；索引不存在不在此区分，由调用方统一报无权。
// 用户不存在报 role.ErrUserNotFound；匹配集为空包装 ErrForbidden。
func (rv *Resolver) Resolve(user, index string) (*View, error) {
	if len(user) < 1 || len(user) > 64 {
		return nil, fmt.Errorf("%w: user name", role.ErrInvalid)
	}
	if len(index) < 1 || len(index) > 64 {
		return nil, fmt.Errorf("%w: index name", role.ErrInvalid)
	}
	bound, ok := rv.reg.UserBound(user)
	if !ok {
		return nil, fmt.Errorf("%w: %s", role.ErrUserNotFound, user)
	}
	v := &View{}
	for _, roleName := range bound {
		entries, exists := rv.reg.RoleEntries(roleName)
		if !exists {
			continue
		}
		v.touched += len(entries)
		for i := range entries {
			en := entries[i]
			if !role.MatchPattern(en.Pattern, index) {
				continue
			}
			v.matched = append(v.matched, en)
			if en.Filter == nil {
				v.DocsOpen = true
			} else {
				v.Filters = append(v.Filters, en.Filter)
			}
			if en.Fields.Unrestricted {
				v.FieldsOpen = true
			} else {
				v.fieldAuths = append(v.fieldAuths, en.Fields)
			}
		}
	}
	if len(v.matched) == 0 {
		return nil, fmt.Errorf("%w: user=%s index=%s", ErrForbidden, user, index)
	}
	if v.DocsOpen {
		v.Filters = nil
	}
	if v.FieldsOpen {
		v.fieldAuths = nil
	}
	return v, nil
}
