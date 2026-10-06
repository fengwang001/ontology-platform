package duty

import "sort"

// EarliestReport 查询人员从 at 起下一次最早可报到时刻。
// 候选值勤期使用该报到时刻、该航段数对应的完整单次上限长度（不含延长），
// 并要求所需资质覆盖其解除时刻。找到返回 (minute,true)，人员不存在返回 (0,false)。
// 结果精确：通过枚举全部分段线性折点保证不漏解。
func (s *System) EarliestReport(personID, at, legs, aircraft int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.people[personID]
	if !ok || legs < 0 || legs > 8 || at < 0 || aircraft < 0 {
		return 0, false
	}
	if _, held := p.quals[aircraft]; !held {
		return 0, false
	}
	expiry := p.quals[aircraft]
	c := s.cfg
	ds := p.duties

	// G：远超一切既有值勤期的保证可行点，用于终止枚举与保证收敛。
	lastEnd := 0
	if n := len(ds); n > 0 {
		lastEnd = ds[n-1].End
	}
	G := lastEnd
	if c.Window28 > G {
		G = c.Window28
	}
	if at > G {
		G = at
	}
	G += c.Window28 + c.Limit28 + c.MinRest + 1

	// 限制枚举范围到与 [at, G] 有关的值勤期（数量由累计上限界定为常数）。
	rel := selectWindow(ds, 0, G+c.Window28)

	cand := map[int]struct{}{at: {}, G: {}}
	add := func(x int) {
		if x >= at && x <= G {
			cand[x] = struct{}{}
		}
	}
	for _, d := range rel {
		add(d.End)
		add(d.End + c.RestRequired(d.Length()))
		add(d.Start - c.MinRest)
		// 后继休息：s + L(s) + RestRequired(L(s)) <= d.Start。RestRequired 取
		// max(MinRest, L)，两种情形的分界点分别枚举（L 随时段变化）。
		for per := 0; per < 3; per++ {
			L := c.singleLimitPeriod(per, legs)
			add(d.Start - L - c.RestRequired(L))
		}
		// 若新值勤期在前，则其后的休息由新时长决定，长度依赖 s 的时段：
		// 边界点 d.Start - SingleLimit(s,legs) 也会在下方按时段枚举覆盖。
	}
	// 滚动窗（7 日、28 日）：候选 s 变化时，候选区间与各折点窗的重叠
	// 关系在 s = 折点(+/-)区间端点处改变。
	addKinks := func(win int) {
		for _, d := range rel {
			// 既有值勤期关于窗起点 w 的四个斜率折点：
			// a-win, a, b-win, b。候选区间 [s, s+L) 的贡献折点为
			// s-win, s, s+L-win, s+L。两族折点“相遇”（相等或差一）时
			// F(s) 结构改变；对齐候选的两条边到每个既有折点及其窗沿，
			// 得到全部候选 s。
			for _, w := range []int{
				d.Start - win, d.Start, d.End - win, d.End,
				d.Start - win + 1, d.Start + 1, d.End - win + 1, d.End + 1,
				d.Start - win - 1, d.Start - 1, d.End - win - 1, d.End - 1,
			} {
				for per := 0; per < 3; per++ {
					L := c.singleLimitPeriod(per, legs)
					// 候选左边 s 对齐 w。
					add(w)
					// 候选左边关于窗沿：s-win 对齐 w。
					add(w + win)
					// 候选右边 s+L 对齐 w。
					add(w - L)
					// 候选右边关于窗沿：s+L-win 对齐 w。
					add(w + win - L)
				}
			}
		}
		// 候选区间自身的四个折点随 s 平移，其与其它窗的关系已被上式覆盖；
		// 另枚举时段边界，覆盖 L(s) 的分段跳变。
	}
	addKinks(c.Window7)
	addKinks(c.Window28)
	// 时段边界（向前后各取一日，覆盖候选解除时刻落入的时段切换）。
	for b := at - c.DayLen - c.Limit28; b <= G+c.DayLen; b++ {
		m := b % c.DayLen
		if m < 0 {
			m += c.DayLen
		}
		if m == c.DayBoundary1 || m == c.DayBoundary2 {
			add(b)
			// 解除时刻 s+L(s) 跨过时段边界时，新值勤期的休息要求随之改变，
			// 故 s = b - L(period) 也是折点（三种时段各一枚举）。
			for per := 0; per < 3; per++ {
				add(b - c.singleLimitPeriod(per, legs))
			}
		}
	}

	pts := make([]int, 0, len(cand))
	for x := range cand {
		pts = append(pts, x)
	}
	sort.Ints(pts)
	var best int = -1
	for _, x := range pts {
		if x < at || x >= expiry {
			continue
		}
		L := c.SingleLimit(x, legs)
		if s.feasibleLocked(p, x, x+L, legs, aircraft) {
			best = x
			break
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// singleLimitPeriod 为指定时段（而非时刻）计算单次上限，供查询枚举使用。
func (c Config) singleLimitPeriod(period, legs int) int {
	l := c.BaseLimit[period] - legs*c.PerLegCut
	if l < c.MinLimit {
		l = c.MinLimit
	}
	return l
}

// feasibleLocked 报告在 at 时刻、候选区间 [ns,ne) 作为新登记是否满足
// 资质、重叠、休息、单次、7/28 日累计全部规则。调用方持锁。
func (s *System) feasibleLocked(p *person, ns, ne, legs, aircraft int) bool {
	pos := findPos(p.duties, ns)
	if pos < len(p.duties) && p.duties[pos].Start == ns {
		return false
	}
	return s.checkRegister(p, pos, ns, ne, legs, aircraft).OK()
}
