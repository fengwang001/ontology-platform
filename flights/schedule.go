package flights

import (
	"cmp"
	"slices"
)

// leg 航班表中的一段，附带引擎内部状态。
type leg struct {
	Flight
	order    int // 全局拓扑序（按计划起飞时刻、ID 排序）
	aIdx     int // 在飞机链中的下标
	cIdx     int // 在机组链中的下标
	delaySum int // 生效延误注入之和
	cancels  int // 生效取消注入数量
	frozen   bool
	st       legState
}

func validateConfig(cfg Config) error {
	if cfg.MinTurnaround < 0 || cfg.MinConnection < 0 || cfg.MaxDuty < 0 {
		return ErrInvalidParam
	}
	for airport, c := range cfg.Curfews {
		if airport == "" || c.Start < 0 || c.End < 0 || c.End < c.Start {
			return ErrInvalidParam
		}
	}
	return nil
}

// buildLegs 校验航班表，构建飞机链与机组链，返回按全局拓扑序排列的航段。
func buildLegs(flights []Flight) (sorted []*leg, byID map[string]*leg, aChains, cChains map[string][]*leg, err error) {
	byID = make(map[string]*leg, len(flights))
	legs := make([]*leg, 0, len(flights))
	for _, f := range flights {
		if f.ID == "" || f.SchedDep < 0 || f.Duration < 0 ||
			f.Origin == "" || f.Dest == "" || f.Aircraft == "" || f.Crew == "" {
			return nil, nil, nil, nil, ErrInvalidParam
		}
		if _, dup := byID[f.ID]; dup {
			return nil, nil, nil, nil, ErrInvalidParam
		}
		l := &leg{Flight: f}
		legs = append(legs, l)
		byID[f.ID] = l
	}

	build := func(key func(*leg) string) (map[string][]*leg, error) {
		chains := map[string][]*leg{}
		for _, l := range legs {
			k := key(l)
			chains[k] = append(chains[k], l)
		}
		for _, ch := range chains {
			slices.SortFunc(ch, func(a, b *leg) int { return cmp.Compare(a.SchedDep, b.SchedDep) })
			for i := 1; i < len(ch); i++ {
				// 链内计划起飞时刻必须严格递增，且到达机场等于下一段起飞机场。
				if ch[i].SchedDep == ch[i-1].SchedDep || ch[i-1].Dest != ch[i].Origin {
					return nil, ErrInvalidParam
				}
			}
		}
		return chains, nil
	}

	if aChains, err = build(func(l *leg) string { return l.Aircraft }); err != nil {
		return nil, nil, nil, nil, err
	}
	if cChains, err = build(func(l *leg) string { return l.Crew }); err != nil {
		return nil, nil, nil, nil, err
	}
	for _, ch := range aChains {
		for i, l := range ch {
			l.aIdx = i
		}
	}
	for _, ch := range cChains {
		for i, l := range ch {
			l.cIdx = i
		}
	}

	sorted = slices.Clone(legs)
	slices.SortFunc(sorted, func(a, b *leg) int {
		if c := cmp.Compare(a.SchedDep, b.SchedDep); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	for i, l := range sorted {
		l.order = i
	}
	return sorted, byID, aChains, cChains, nil
}
