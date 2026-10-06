package duty

// 区间均为左闭右开整数分钟区间。

// overlap 返回区间 [a,b) 与 [l,r) 的整数分钟重叠长度（左闭右开）。
func overlap(a, b, l, r int) int {
	lo := a
	if l > lo {
		lo = l
	}
	hi := b
	if r < hi {
		hi = r
	}
	if hi <= lo {
		return 0
	}
	return hi - lo
}

type slopeEvent struct {
	w, d int
}

// rollingViolation 在"已有值勤期列表 ds（按 Start 升序、互不重叠）"的基础上，
// 假设加入/替换为候选区间 [ns,ne)，返回使长度为 win 的整数起点时间窗内
// 累计值勤超过 cap 的最小窗起点；无超限返回 -1。
//
// 只考察与候选区间相关的窗；窗起点枚举为候选区间可见范围内的全部折点，
// 这是精确的：累计函数关于窗起点分段线性，极值只出现在折点/端点。
// F(w) = 候选区间 [ns,ne) 与 ds 中所有区间对窗 [w, w+win) 的重叠总时长。
// F 关于整数窗起点 w 分段线性：
//   - 任一区间 [a,b) 贡献 overlap(a,b,w,w+win)，斜率在 w=a-win、a、b-win、b
//     处分别发生 +1、-1、-1、+1 的跳变（其中落在整数域上的点）。
//
// 因此超限（F(w) > capLimit）一旦出现，其最小整数 w 只可能是：
//   - 考察区间的左端点 lo，或
//   - 某个斜率由 <=0 变为 >0 的折点本身（上抬段起点），或
//   - 上抬段起点的下一个整数点（折点位于半整数时）。
//
// 其余区段上 F 不增，不可能成为“首个”超限位。
//
// lo/hi 取候选区间与任意既有区间发生作用的窗起点范围；此范围外候选区间
// 与窗无重叠，且既有区间若有重叠也不可能成为新超限（系统原本即合规）。
func rollingViolation(ds []*Duty, ns, ne, win, capLimit int) int {
	lo := ns - win
	if lo < 0 {
		lo = 0
	}
	hi := ne // w == ne 起候选区间不再重叠；hi 作为排他上界

	events := make([]slopeEvent, 0, 4*len(ds)+4)
	add := func(w, d int) {
		events = append(events, slopeEvent{w, d})
	}
	// 候选区间。
	add(ns-win, +1)
	add(ns, -1)
	add(ne-win, -1)
	add(ne, +1)
	// 与窗可能重叠的既有区间只需落在 [lo, hi+win) 附近；调用方传入的 ds
	// 已经过位置筛选，这里仍以范围裁剪事件，保持本函数自足。
	for _, d := range ds {
		add(d.Start-win, +1)
		add(d.Start, -1)
		add(d.End-win, -1)
		add(d.End, +1)
	}

	type iv struct{ a, b int }
	ints := append(make([]iv, 0, len(ds)+1), iv{ns, ne})
	for _, d := range ds {
		ints = append(ints, iv{d.Start, d.End})
	}
	sumAt := func(w int) int {
		total := 0
		for _, x := range ints {
			total += overlap(x.a, x.b, w, w+win)
		}
		return total
	}
	// s(w) = F(w+1)-F(w)：窗右沿进入某区间 +1，窗左沿离开某区间 -1。
	slopeAt := func(w int) int {
		s := 0
		for _, x := range ints {
			if x.a <= w+win && w+win < x.b {
				s++
			}
			if x.a <= w && w < x.b {
				s--
			}
		}
		return s
	}

	sortEvents(events)
	// 合并折点。
	merged := events[:0]
	for _, e := range events {
		if len(merged) > 0 && merged[len(merged)-1].w == e.w {
			merged[len(merged)-1].d += e.d
		} else {
			merged = append(merged, e)
		}
	}

	// 逐段推进 [prev, nextKink) 上的线性函数 F；上升段中首个越界点
	// 为 prev + ceil((cap+1-F(prev))/slope)。
	prev := lo
	F := sumAt(lo)
	slope := slopeAt(lo)
	riseHit := func(next int) int {
		if F > capLimit {
			return prev
		}
		if slope > 0 {
			need := capLimit + 1 - F
			step := (need + slope - 1) / slope
			if step < 0 {
				step = 0
			}
			if t := prev + step; t < next {
				return t
			}
		}
		return -1
	}
	for _, e := range merged {
		k := e.w
		if k <= lo {
			// 折点在 lo 之前：只影响 lo 处斜率，直接重算更简单。
			slope = slopeAt(lo)
			continue
		}
		if k >= hi {
			break
		}
		if t := riseHit(k); t >= 0 {
			return t
		}
		// 推进到折点 k，应用折点后的斜率增量。
		F += slope * (k - prev)
		if F > capLimit {
			return k
		}
		slope += e.d
		prev = k
	}
	if t := riseHit(hi); t >= 0 {
		return t
	}
	return -1
}

func sortEvents(evs []slopeEvent) {
	for i := 1; i < len(evs); i++ {
		for j := i; j > 0 && evs[j-1].w > evs[j].w; j-- {
			evs[j-1], evs[j] = evs[j], evs[j-1]
		}
	}
}

// dutyOverlapSum 计算 ds 与窗 [w,w+win) 的重叠总时长（朴素遍历，测试对照用）。
func dutyOverlapSum(ds []*Duty, w, win int) int {
	total := 0
	for _, d := range ds {
		total += overlap(d.Start, d.End, w, w+win)
	}
	return total
}
