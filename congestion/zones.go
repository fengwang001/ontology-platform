package congestion

// registry 保存区域定义。
type registry struct {
	zones map[string]*Zone
}

func newRegistry() *registry {
	return &registry{zones: map[string]*Zone{}}
}

func validZone(z *Zone) bool {
	if z == nil || z.ID == "" || z.DailyFee < 0 || len(z.Cells) == 0 {
		return false
	}
	return z.StartHHMM >= 0 && z.EndHHMM > z.StartHHMM && z.EndHHMM <= 24*60
}

// add 校验嵌套关系：与任意已有区域要么一个完全包含另一个，要么不相交。
func (r *registry) add(z *Zone) error {
	for _, ex := range r.zones {
		if relateCells(z.Cells, ex.Cells) == relOverlap {
			return ErrZonesOverlap
		}
	}
	cp := &Zone{ID: z.ID, DailyFee: z.DailyFee, StartHHMM: z.StartHHMM, EndHHMM: z.EndHHMM,
		Cells: map[string]bool{}}
	for c := range z.Cells {
		cp.Cells[c] = true
	}
	r.zones[z.ID] = cp
	return nil
}

func (r *registry) get(id string) (*Zone, bool) {
	z, ok := r.zones[id]
	return z, ok
}

// ancestors 返回进入 zoneID 时同时视为进入的全部外层（不含自身）。
func (r *registry) ancestors(zoneID string) []string {
	var out []string
	inner := r.zones[zoneID]
	for id, z := range r.zones {
		if id == zoneID {
			continue
		}
		if relateCells(inner.Cells, z.Cells) == relInnerSubsetOfOuter {
			out = append(out, id)
		}
	}
	return out
}

type cellRelation int

const (
	relDisjoint cellRelation = iota
	relEqual
	relInnerSubsetOfOuter // 第一参数 ⊆ 第二参数
	relOuterSupersetOf    // 第一参数 ⊇ 第二参数
	relOverlap
)

func relateCells(inner, outer map[string]bool) cellRelation {
	innerOutside, outerOutside := false, false
	for k := range inner {
		if !outer[k] {
			innerOutside = true
		}
	}
	for k := range outer {
		if !inner[k] {
			outerOutside = true
		}
	}
	switch {
	case innerOutside && outerOutside:
		// 可能不相交也可能相交但互不包含，调用方按相交与否再区分
		for k := range inner {
			if outer[k] {
				return relOverlap
			}
		}
		return relDisjoint
	case innerOutside && !outerOutside:
		return relOuterSupersetOf // 第一参数 ⊋ 第二参数（含不相交情形不可能，因有交集）
	case !innerOutside && outerOutside:
		return relInnerSubsetOfOuter
	default:
		return relEqual
	}
}
