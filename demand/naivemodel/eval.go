package naivemodel

import "sort"

func (m *Model) criticalPriority() int {
	best := 0
	for _, l := range m.loads {
		if best == 0 || l.spec.Priority < best {
			best = l.spec.Priority
		}
	}
	return best
}

func (m *Model) lastPower() *ratio {
	if len(m.history) == 0 {
		return newRat()
	}
	return newRat().set(m.history[len(m.history)-1].power)
}

// forecastAny 判断 now 时刻、shedKW 切除、addedKW 恢复后，是否有活跃窗口越限。
func (m *Model) forecastAny(now, shedKW, addedKW int64) bool {
	p := m.lastPower()
	p.subInt(shedKW).addInt(addedKW)
	if p.sign() < 0 {
		p.setZero()
	}
	limit := m.cfg.ContractKW * m.cfg.WindowSec
	n := m.cfg.WindowSec / m.cfg.SlipSec
	next := (now/m.cfg.SlipSec + 1) * m.cfg.SlipSec
	for k := int64(0); k < n; k++ {
		end := next + k*m.cfg.SlipSec
		if end <= now {
			continue
		}
		// 预测时窗口未结束：只统计 [end-W, now] 的已发生部分。
		occ := m.energyIn(end-m.cfg.WindowSec, now)
		future := p.clone().mulInt(end - now)
		total := occ.clone().add(future)
		if total.cmpInt(limit) > 0 {
			return true
		}
	}
	return false
}

func (m *Model) evaluate(now int64) *Result {
	res := &Result{At: now}

	if m.forecastAny(now, 0, 0) {
		crit := m.criticalPriority()
		var cands []*load
		for _, l := range m.loads {
			if l.on && !l.locked && l.spec.Priority != crit && now-l.since >= l.spec.MinOnSec {
				cands = append(cands, l)
			}
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].spec.ID < cands[j].spec.ID })

		var total int64
		for _, l := range cands {
			total += l.spec.RatedKW
		}
		if m.forecastAny(now, total, 0) {
			res.StillExceed = true
			for _, l := range cands {
				l.on = false
				l.since = now
				res.Actions = append(res.Actions, Action{Kind: AKindShed, LoadID: l.spec.ID, At: now})
			}
			sortActions(res.Actions)
			return res
		}

		bestMask, bestCnt, bestPri := -1, len(cands)+1, int64(-1)
		for mask := 1; mask < 1<<len(cands); mask++ {
			var kw, pri int64
			cnt := 0
			for i, l := range cands {
				if mask&(1<<i) != 0 {
					kw += l.spec.RatedKW
					pri += int64(l.spec.Priority)
					cnt++
				}
			}
			if m.forecastAny(now, kw, 0) {
				continue
			}
			better := cnt < bestCnt
			if !better && cnt == bestCnt {
				if pri > bestPri {
					better = true
				} else if pri == bestPri && lexLess(maskIDs(mask, cands), maskIDs(bestMask, cands)) {
					better = true
				}
			}
			if better {
				bestMask, bestCnt, bestPri = mask, cnt, pri
			}
		}
		for i, l := range cands {
			if bestMask >= 0 && bestMask&(1<<i) != 0 {
				l.on = false
				l.since = now
				res.Actions = append(res.Actions, Action{Kind: AKindShed, LoadID: l.spec.ID, At: now})
			}
		}
		sortActions(res.Actions)
		return res
	}

	var cands []*load
	for _, l := range m.loads {
		if !l.on && !l.locked && now-l.since >= l.spec.MinOffSec {
			cands = append(cands, l)
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].spec.Priority != cands[j].spec.Priority {
			return cands[i].spec.Priority < cands[j].spec.Priority
		}
		return cands[i].spec.ID < cands[j].spec.ID
	})
	var added int64
	for _, l := range cands {
		if m.forecastAny(now, 0, added+l.spec.RatedKW) {
			break
		}
		added += l.spec.RatedKW
		l.on = true
		l.since = now
		res.Actions = append(res.Actions, Action{Kind: AKindRestore, LoadID: l.spec.ID, At: now})
	}
	sortActions(res.Actions)
	return res
}

func maskIDs(mask int, cands []*load) []int {
	if mask < 0 {
		return nil
	}
	var out []int
	for i, l := range cands {
		if mask&(1<<i) != 0 {
			out = append(out, l.spec.ID)
		}
	}
	sort.Ints(out)
	return out
}

func lexLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
