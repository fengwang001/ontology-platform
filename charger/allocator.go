package charger

import "sort"

// allocate 对全部在站未充满车辆重新分配功率，并原地更新 Power/State。
// 严格按类别：优先类别先分，剩余预算给普通类别。
// 预算为负时视为 0（总上限本身不允许为负，防御性处理）。
func allocate(sessions []*Session, totalCap int) {
	var prefer, normal []*Session
	for _, s := range sessions {
		if !s.active || s.State == StateFull {
			continue
		}
		switch s.Prio {
		case Prefer:
			prefer = append(prefer, s)
		default:
			normal = append(normal, s)
		}
	}
	left := totalCap
	if left < 0 {
		left = 0
	}
	used := allocateClass(prefer, left)
	left -= used
	if left < 0 {
		left = 0
	}
	allocateClass(normal, left)
}

// allocateClass 在预算 budget 内为一个类别分配功率，返回实际占用功率。
// 规则：
//  1. 池内按 capped 分水填充分摊整数预算（余量按插枪先后加一）；
//  2. 分得功率低于自身最低可用功率者，从池中挑出一台转为等待：
//     先挑原等待者、再挑原充电者，同状态内挑最晚插枪者；
//  3. 释放其份额后回到第 1 步重新分摊，直到池中人人达标。
func allocateClass(pool []*Session, budget int) int {
	// orig 记录本轮分配开始时的状态，用于挑受害者。
	orig := make(map[int64]State, len(pool))
	for _, s := range pool {
		orig[s.ID] = s.State
	}

	got := waterfill(pool, budget)

	for {
		var bad []*Session
		for i, s := range pool {
			if got[i] < s.MinP {
				bad = append(bad, s)
			}
		}
		if len(bad) == 0 {
			for i, s := range pool {
				s.Power = got[i]
				if got[i] > 0 {
					s.State = StateCharging
				} else {
					// 未得到任何功率：等待下一事件重分（含 MinP=0 的极端情形）。
					s.State = StateWaiting
				}
			}
			return sumInt(got)
		}

		victim := pickVictim(bad, orig)
		victim.Power = 0
		victim.State = StateWaiting

		next := make([]*Session, 0, len(pool)-1)
		for _, s := range pool {
			if s != victim {
				next = append(next, s)
			}
		}
		pool = next
		got = waterfill(pool, budget)
	}
}

// pickVictim 从不达标车辆中挑一台转为等待：
// 先原本等待者、再原本充电者；同状态内最晚插枪者优先。
func pickVictim(bad []*Session, orig map[int64]State) *Session {
	victim := bad[0]
	for _, s := range bad[1:] {
		if betterVictim(s, victim, orig) {
			victim = s
		}
	}
	return victim
}

// betterVictim 报告候选 a 是否比当前选中 b 更应被挑出。
func betterVictim(a, b *Session, orig map[int64]State) bool {
	wa, wb := orig[a.ID] == StateWaiting, orig[b.ID] == StateWaiting
	if wa != wb {
		return wa // 原等待者优先被挑
	}
	return a.PlugAt > b.PlugAt // 同状态：最晚插枪者优先
}

// waterfill 在给定池内按各自自身上限 selfCap 做整数 capped 分水填充。
// 先按封顶线升序逐档封顶；在某一档预算不足时，未封顶者齐平抬到
// level=已用档位+余量/人数，余数按插枪先后依次每台加一。
// 返回与 pool 对齐的功率切片；pool 为空或预算非正时返回全 0。
func waterfill(pool []*Session, budget int) []int {
	got := make([]int, len(pool))
	if len(pool) == 0 || budget <= 0 {
		return got
	}

	order := make([]int, len(pool))
	for i := range order {
		order[i] = i
	}
	// 按上限升序封顶；同上限按插枪先后，保证确定性。
	sort.SliceStable(order, func(a, b int) bool {
		x, y := pool[order[a]], pool[order[b]]
		if x.selfCap != y.selfCap {
			return x.selfCap < y.selfCap
		}
		return x.PlugAt < y.PlugAt
	})

	// 逐档消耗预算，确定停止档位与剩余余量；最后一次性写入结果。
	rem := budget
	prevLevel := 0
	n := len(order)
	i := 0
	for i < n {
		threshold := pool[order[i]].selfCap
		k := 0
		for i+k < n && pool[order[i+k]].selfCap == threshold {
			k++
		}
		liveCount := n - i // 本档开始时尚未封顶的人数
		need := liveCount * (threshold - prevLevel)
		if rem < need {
			// 在 (prevLevel, threshold) 之间停下：此前 0..i-1 已封顶，
			// i..n-1 抬到 level，余数按插枪先后逐台 +1。
			for j := 0; j < i; j++ {
				got[order[j]] = pool[order[j]].selfCap
			}
			level := prevLevel + rem/liveCount
			extra := rem % liveCount
			rest := append([]int(nil), order[i:]...)
			for _, j := range rest {
				got[j] = level
			}
			sort.SliceStable(rest, func(a, b int) bool {
				if pool[rest[a]].PlugAt != pool[rest[b]].PlugAt {
					return pool[rest[a]].PlugAt < pool[rest[b]].PlugAt
				}
				return pool[rest[a]].ID < pool[rest[b]].ID
			})
			for r := 0; r < extra; r++ {
				got[rest[r]]++
			}
			return got
		}
		rem -= need
		prevLevel = threshold
		i += k
	}

	// 预算充足：全部封顶。
	for j := 0; j < n; j++ {
		got[order[j]] = pool[order[j]].selfCap
	}
	return got
}

func sumInt(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}
