package ontology

// 本文件实现单个病例的接触者推导。
// derive 是“当前全部住宿登记 + 病例登记 + now”的纯函数：
// 不读取任何历史推导结果，因此无论追补、改正、撤销的先后顺序如何，
// 相同登记集合与 now 必然得到相同结果。

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// ensureFresh 保证病例缓存对给定 now 是最新的；必要时重新推导。
func (e *Engine) ensureFresh(c *caseRec, now int64) {
	if c.revoked {
		return
	}
	if c.valid && !(c.nowSensitive && c.derivedAt != now) {
		return
	}
	e.derive(c, now)
	e.stats.CasesDerived++
}

// derive 重新推导病例 c 在 now 的密切接触者与次密接，并维护 closeOf 提示索引。
func (e *Engine) derive(c *caseRec, now int64) {
	oldClose := c.close

	lo := c.infectiousStart()
	hi := now
	if c.isolated {
		hi = c.isolatedAt
	}

	sawOpen := false
	closeAcc := make(map[string]*accInfo)

	// 第一阶段：密切接触者。
	// 与病例患者在同一病房、且处于传染期 [lo, hi) 内的重叠时长累计。
	if hi > lo {
		for _, s := range e.stays.ofPatient(c.patient) {
			e.stats.StaysScanned++
			if s.open {
				sawOpen = true
			}
			a := max64(s.in, lo)
			b := min64(s.end(now), hi)
			if a >= b {
				continue
			}
			for _, t := range e.stays.ofWard(s.ward) {
				e.stats.StaysScanned++
				if t.patient == c.patient {
					continue
				}
				if t.open {
					sawOpen = true
				}
				oa := max64(a, t.in)
				ob := min64(b, t.end(now))
				if oa >= ob {
					continue
				}
				acc := closeAcc[t.patient]
				if acc == nil {
					acc = &accInfo{first: oa, last: ob}
					closeAcc[t.patient] = acc
				}
				acc.minutes += ob - oa
				if oa < acc.first {
					acc.first = oa
				}
				if ob > acc.last {
					acc.last = ob
				}
			}
		}
	}

	c.close = make(map[string]accInfo)
	for p, acc := range closeAcc {
		if acc.minutes >= ContactThresholdMinutes {
			c.close[p] = *acc
		}
	}

	// 第二阶段：次密接。
	// 对每个密切接触者 q，暴露期为 [首个重叠起点, 确诊登记时刻)；
	// 与他人在暴露期内同病房累计 >= 阈值者（非密接、非病例本人）为次密接。
	type pair struct {
		x, q string
	}
	secAcc := make(map[pair]*accInfo)
	for q, qa := range c.close {
		expLo := qa.first
		expHi := c.registeredAt
		if expHi <= expLo {
			continue
		}
		for _, s := range e.stays.ofPatient(q) {
			e.stats.StaysScanned++
			a := max64(s.in, expLo)
			b := min64(s.end(now), expHi)
			if a >= b {
				continue
			}
			for _, t := range e.stays.ofWard(s.ward) {
				e.stats.StaysScanned++
				if t.patient == q || t.patient == c.patient {
					continue
				}
				oa := max64(a, t.in)
				ob := min64(b, t.end(now))
				if oa >= ob {
					continue
				}
				k := pair{x: t.patient, q: q}
				acc := secAcc[k]
				if acc == nil {
					acc = &accInfo{first: oa, last: ob}
					secAcc[k] = acc
				}
				acc.minutes += ob - oa
				if ob > acc.last {
					acc.last = ob
				}
			}
		}
	}

	c.secondary = make(map[string]secInfo)
	for k, acc := range secAcc {
		if acc.minutes < ContactThresholdMinutes {
			continue
		}
		if _, isClose := c.close[k.x]; isClose {
			continue
		}
		cur, ok := c.secondary[k.x]
		// 同一患者可能经多个密接达标：取累计最大者，并列时取字典序较小的经由者，
		// 保证结果与遍历顺序无关。
		if !ok || acc.minutes > cur.minutes ||
			(acc.minutes == cur.minutes && k.q < cur.via) {
			last := acc.last
			if ok && cur.last > last {
				last = cur.last
			}
			c.secondary[k.x] = secInfo{minutes: acc.minutes, last: last, via: k.q}
		} else if acc.last > cur.last {
			cur.last = acc.last
			c.secondary[k.x] = cur
		}
	}

	// 维护 closeOf 提示索引（用于住宿变更时的精确失效）。
	for p := range oldClose {
		if _, still := c.close[p]; !still {
			if set := e.closeOf[p]; set != nil {
				delete(set, c.id)
				if len(set) == 0 {
					delete(e.closeOf, p)
				}
			}
		}
	}
	for p := range c.close {
		set := e.closeOf[p]
		if set == nil {
			set = make(map[string]bool)
			e.closeOf[p] = set
		}
		set[c.id] = true
	}

	c.nowSensitive = !c.isolated && sawOpen
	c.derivedAt = now
	c.valid = true
}
