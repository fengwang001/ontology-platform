package dls

import "ontology/role"

// Fields 为裁剪视图中的文档字段。
type Fields map[string]role.Value

// View 为用户在某索引上的裁剪视图。
// 文档侧与字段侧各自独立合并，互不绑定。
type View struct {
	// touched 为本次权限判定触碰的授权条目数（非导出，测试证明其上限）。
	touched int

	docUnrestricted bool
	filters         []*role.Expr

	fieldUnrestricted bool
	fieldAuths        []role.FieldAuth
}

// Resolve 从用户各角色的条目快照构建其在索引 index 上的裁剪视图。
// roleGroups 仅含该用户绑定的角色；M 为其中索引模式匹配 index 的条目集。
// M 为空（含索引不存在的情形）返回 role.ErrNoPerm。
func Resolve(roleGroups [][]role.Entry, index string) (*View, error) {
	view := &View{}
	for _, entries := range roleGroups {
		for i := range entries {
			entry := &entries[i]
			view.touched++
			if !role.MatchPattern(entry.IndexPattern, index) {
				continue
			}
			if entry.Filter == nil {
				view.docUnrestricted = true
			} else {
				view.filters = append(view.filters, entry.Filter)
			}
			if entry.Fields.Unrestricted {
				view.fieldUnrestricted = true
			} else {
				view.fieldAuths = append(view.fieldAuths, entry.Fields)
			}
		}
	}
	if !view.docUnrestricted && len(view.filters) == 0 {
		return nil, role.ErrNoPerm
	}
	if !view.fieldUnrestricted && len(view.fieldAuths) == 0 {
		return nil, role.ErrNoPerm
	}
	return view, nil
}

// DocVisible 判断完整文档在文档侧是否可见。
// 任一匹配条目 filter 为空即无限制；否则可见文档为各条 filter 的并集。
// filter 总在完整文档上求值，不受字段可见性影响。
func (v *View) DocVisible(fields map[string]role.Value) bool {
	if v.docUnrestricted {
		return true
	}
	for _, filter := range v.filters {
		if role.Eval(*filter, fields) {
			return true
		}
	}
	return false
}

// FieldVisible 判断字段在字段侧是否可见。
// 任一条目字段授权为无限制即无限制；否则为各条可见字段的并集；
// except 只扣减其所在条目的 grant。
func (v *View) FieldVisible(field string) bool {
	if v.fieldUnrestricted {
		return true
	}
	for _, auth := range v.fieldAuths {
		if auth.FieldVisible(field) {
			return true
		}
	}
	return false
}

// StripFields 返回仅含可见字段的新映射（裁剪视图）。
func (v *View) StripFields(fields map[string]role.Value) Fields {
	out := Fields{}
	if v.fieldUnrestricted {
		for name, value := range fields {
			out[name] = value
		}
		return out
	}
	for name, value := range fields {
		if v.FieldVisible(name) {
			out[name] = value
		}
	}
	return out
}

// Touched 返回权限判定触碰的授权条目数（测试用证明）。
func (v *View) Touched() int { return v.touched }
