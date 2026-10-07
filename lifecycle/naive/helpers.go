package naive

import "sort"

func sortInts(x []int) { sort.Ints(x) }

func ensureLink(cl map[InstanceID]map[LinkType]map[InstanceID]bool, src InstanceID, link LinkType) {
	if cl[src] == nil {
		cl[src] = map[LinkType]map[InstanceID]bool{}
	}
	if cl[src][link] == nil {
		cl[src][link] = map[InstanceID]bool{}
	}
}

func sortedDsts(set map[InstanceID]bool) []InstanceID {
	out := make([]InstanceID, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func cloneLinks(cl map[InstanceID]map[LinkType]map[InstanceID]bool) map[InstanceID]map[LinkType]map[InstanceID]bool {
	cp := map[InstanceID]map[LinkType]map[InstanceID]bool{}
	for s, byLink := range cl {
		cp[s] = map[LinkType]map[InstanceID]bool{}
		for lk, dsts := range byLink {
			cp[s][lk] = map[InstanceID]bool{}
			for d := range dsts {
				cp[s][lk][d] = true
			}
		}
	}
	return cp
}

func cloneOut(in map[LinkType]map[InstanceID]bool) map[LinkType]map[InstanceID]bool {
	if in == nil {
		return nil
	}
	cp := map[LinkType]map[InstanceID]bool{}
	for lk, dsts := range in {
		cp[lk] = map[InstanceID]bool{}
		for d := range dsts {
			cp[lk][d] = true
		}
	}
	return cp
}

func neighbors(cl map[InstanceID]map[LinkType]map[InstanceID]bool, src InstanceID, link LinkType) []InstanceID {
	if cl[src] == nil {
		return nil
	}
	return sortedDsts(cl[src][link])
}

func asInt64(v AttrValue) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

func cmpVal(a, b AttrValue) (int, bool) {
	if na, ok := asInt64(a); ok {
		if nb, ok := asInt64(b); ok {
			switch {
			case na < nb:
				return -1, true
			case na > nb:
				return 1, true
			default:
				return 0, true
			}
		}
	}
	if a == b {
		return 0, true
	}
	return 0, false
}

func evalAttr(in *inst, c *AttrCheck) bool {
	got, ok := in.attrs[c.Key]
	if !ok {
		return false
	}
	r, ok := cmpVal(got, c.Value)
	if !ok {
		return false
	}
	switch c.Op {
	case "eq":
		return r == 0
	case "ne":
		return r != 0
	case "lt":
		return r < 0
	case "le":
		return r <= 0
	case "gt":
		return r > 0
	case "ge":
		return r >= 0
	}
	return false
}

func inStates(s State, set []State) bool {
	for _, x := range set {
		if x == s {
			return true
		}
	}
	return false
}

// evalPre 在当前副本上评估全部前置条件。
func (m *Model) evalPre(id InstanceID, r *Rule,
	ci map[InstanceID]*inst, cl map[InstanceID]map[LinkType]map[InstanceID]bool) bool {
	in := ci[id]
	for _, pc := range r.Preconditions {
		switch {
		case pc.Attr != nil:
			if !evalAttr(in, pc.Attr) {
				return false
			}
		case pc.LinkCount != nil:
			n := len(neighbors(cl, id, pc.LinkCount.Link))
			if pc.LinkCount.Min >= 0 && n < pc.LinkCount.Min {
				return false
			}
			if pc.LinkCount.Max >= 0 && n > pc.LinkCount.Max {
				return false
			}
		case pc.LinkState != nil:
			nbs := neighbors(cl, id, pc.LinkState.Link)
			if len(nbs) == 0 {
				return false
			}
			if pc.LinkState.RequireAll {
				for _, nb := range nbs {
					if !inStates(ci[nb].state, pc.LinkState.AllowedStates) {
						return false
					}
				}
			} else {
				any := false
				for _, nb := range nbs {
					if inStates(ci[nb].state, pc.LinkState.AllowedStates) {
						any = true
					}
				}
				if !any {
					return false
				}
			}
		}
	}
	return true
}

// evalPost 在“已推进状态”的副本上评估属性边界、迁移后基数与跨实例钩子。
func (m *Model) evalPost(id InstanceID, r *Rule,
	ci map[InstanceID]*inst, cl map[InstanceID]map[LinkType]map[InstanceID]bool) int {
	in := ci[id]
	for _, b := range r.AttrBounds {
		v, exists := in.attrs[b.Key]
		ok := false
		if b.Min != nil || b.Max != nil {
			if n, isNum := asInt64(v); exists && isNum {
				ok = true
				if b.Min != nil && n < *b.Min {
					ok = false
				}
				if b.Max != nil && n > *b.Max {
					ok = false
				}
			}
		} else if exists {
			for _, allowed := range b.AllowedValues {
				if c, comparable := cmpVal(v, allowed); comparable && c == 0 {
					ok = true
				}
			}
		}
		if !ok {
			return ErrPrecondition
		}
	}
	for _, cb := range r.Cardinality {
		n := len(neighbors(cl, id, cb.Link))
		if n < cb.Min || (cb.Max >= 0 && n > cb.Max) {
			return ErrCardinality
		}
	}
	for _, h := range r.Hooks {
		for _, nb := range neighbors(cl, id, h.Link) {
			if !inStates(ci[nb].state, h.RequireStates) {
				return ErrHook
			}
		}
	}
	return 0
}
