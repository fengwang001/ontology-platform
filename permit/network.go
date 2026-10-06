package permit

// networkView 是路网不可变视图，提供绕行正反向索引。
type networkView struct {
	segments map[string]*Segment
	// corridorCap 走廊 ID -> 同时封闭许可数上限。
	corridorCap map[string]int
	// detourOf 路段 ID -> 指定绕行路线（路段 ID 序列）。
	detourOf map[string][]string
	// incomingDetour 路段 x -> 所有把 x 纳入绕行路线的路段集合。
	incomingDetour map[string]map[string]struct{}
}

func buildNetwork(n Network, corridorCap map[string]int) (*networkView, *RuleError) {
	v := &networkView{
		segments:       map[string]*Segment{},
		corridorCap:    map[string]int{},
		detourOf:       map[string][]string{},
		incomingDetour: map[string]map[string]struct{}{},
	}
	for i := range n.Segments {
		seg := n.Segments[i]
		if seg.ID == "" || seg.Lanes <= 0 || seg.Corridor == "" {
			return nil, ruleErr(ErrInvalidParam, "segment requires non-empty id/corridor and positive lanes", seg.ID)
		}
		if _, dup := v.segments[seg.ID]; dup {
			return nil, ruleErr(ErrInvalidParam, "duplicate segment id", seg.ID)
		}
		v.segments[seg.ID] = &seg
	}
	// 走廊上限：配置中出现的上限必须为正；未配置的走廊默认为无限制（0 表示不限）。
	for c, cap := range corridorCap {
		if cap <= 0 {
			return nil, ruleErr(ErrInvalidParam, "corridor cap must be positive", c)
		}
		v.corridorCap[c] = cap
	}
	for id, seg := range v.segments {
		route := make([]string, 0, len(seg.Detour))
		for _, d := range seg.Detour {
			if _, ok := v.segments[d]; !ok {
				return nil, ruleErr(ErrSegmentNotFound, "detour references unknown segment", id, d)
			}
			if d == id {
				return nil, ruleErr(ErrInvalidParam, "detour must consist of other segments", id)
			}
			route = append(route, d)
		}
		v.detourOf[id] = route
		for _, d := range route {
			if v.incomingDetour[d] == nil {
				v.incomingDetour[d] = map[string]struct{}{}
			}
			v.incomingDetour[d][id] = struct{}{}
		}
	}
	return v, nil
}
