package delegation

// Clone 返回 ObjectPerm 的深拷贝。
func (p ObjectPerm) Clone() ObjectPerm {
	out := ObjectPerm{}
	if len(p.Attrs) > 0 {
		out.Attrs = make(map[string]bool, len(p.Attrs))
		for k := range p.Attrs {
			out.Attrs[k] = true
		}
	}
	if len(p.Rows) > 0 {
		out.Rows = make(map[string]bool, len(p.Rows))
		for k := range p.Rows {
			out.Rows[k] = true
		}
	}
	return out
}

// IsSubsetOf 报告 p 是否为 q 的子集（属性与行两个维度同时满足）。
func (p ObjectPerm) IsSubsetOf(q ObjectPerm) bool {
	for a := range p.Attrs {
		if !q.Attrs[a] {
			return false
		}
	}
	for r := range p.Rows {
		if !q.Rows[r] {
			return false
		}
	}
	return true
}

// IsEmpty 报告 p 是否不包含任何权限。
func (p ObjectPerm) IsEmpty() bool {
	return len(p.Attrs) == 0 && len(p.Rows) == 0
}

// add 将 q 并入 p（原地并集）。
func (p ObjectPerm) add(q ObjectPerm) ObjectPerm {
	if len(q.Attrs) > 0 && p.Attrs == nil {
		p.Attrs = make(map[string]bool, len(q.Attrs))
	}
	for a := range q.Attrs {
		p.Attrs[a] = true
	}
	if len(q.Rows) > 0 && p.Rows == nil {
		p.Rows = make(map[string]bool, len(q.Rows))
	}
	for r := range q.Rows {
		p.Rows[r] = true
	}
	return p
}

// remove 从 p 中扣除 q（原地差集）。
func (p ObjectPerm) remove(q ObjectPerm) ObjectPerm {
	for a := range q.Attrs {
		delete(p.Attrs, a)
	}
	for r := range q.Rows {
		delete(p.Rows, r)
	}
	return p
}

// Clone 返回 PermissionSet 的深拷贝。
func (s PermissionSet) Clone() PermissionSet {
	out := make(PermissionSet, len(s))
	for ot, p := range s {
		out[ot] = p.Clone()
	}
	return out
}

// IsSubsetOf 报告 s 的每个对象类型权限是否都是 other 对应权限的子集。
func (s PermissionSet) IsSubsetOf(other PermissionSet) bool {
	for ot, p := range s {
		q, ok := other[ot]
		if !ok {
			if !p.IsEmpty() {
				return false
			}
			continue
		}
		if !p.IsSubsetOf(q) {
			return false
		}
	}
	return true
}

// Union 返回两个权限集的并集（不修改入参）。
func (s PermissionSet) Union(other PermissionSet) PermissionSet {
	out := s.Clone()
	for ot, p := range other {
		out[ot] = out[ot].add(p)
	}
	return out
}

// Subtract 返回从 s 中扣除 other 后的权限集（不修改入参）。
func (s PermissionSet) Subtract(other PermissionSet) PermissionSet {
	out := s.Clone()
	for ot, p := range other {
		if cur, ok := out[ot]; ok {
			out[ot] = cur.remove(p)
		}
	}
	return out
}

// Intersect 返回两个权限集的交集（不修改入参）。
func (s PermissionSet) Intersect(other PermissionSet) PermissionSet {
	out := PermissionSet{}
	for ot, p := range s {
		q, ok := other[ot]
		if !ok {
			continue
		}
		inter := ObjectPerm{}
		for a := range p.Attrs {
			if q.Attrs[a] {
				if inter.Attrs == nil {
					inter.Attrs = map[string]bool{}
				}
				inter.Attrs[a] = true
			}
		}
		for r := range p.Rows {
			if q.Rows[r] {
				if inter.Rows == nil {
					inter.Rows = map[string]bool{}
				}
				inter.Rows[r] = true
			}
		}
		if !inter.IsEmpty() {
			out[ot] = inter
		}
	}
	return out
}
