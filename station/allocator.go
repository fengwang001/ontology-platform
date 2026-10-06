package station

type allocCause int

const (
	causeOther allocCause = iota
	causeCapDown
)

// allocPlan 是一次重新分配的结果：sessionID -> 功率（仅含被接纳车辆）。
type allocPlan map[string]int

// reallocCtx 携带单次重新分配的上下文。
type reallocCtx struct {
	cause   allocCause
	demoted map[string]bool // 本次事件中被调低优先级的会话（自身可被挤为等待）
}

// reallocate 先分配优先类别，剩余功率再分配普通类别，并据此更新会话状态。
func (s *Station) reallocate(cause allocCause) {
	s.reallocateCtx(reallocCtx{cause: cause})
}

func (s *Station) reallocateCtx(ctx reallocCtx) {
	oldFastUsed := 0
	for _, se := range s.sessions {
		if se.Priority == PriorityFast && se.State == StateCharging {
			oldFastUsed += se.Pwr
		}
	}
	fastPlan := s.allocClass(PriorityFast, s.cap, ctx.cause == causeCapDown, ctx.demoted)
	fastUsed := 0
	for _, p := range fastPlan {
		fastUsed += p
	}
	// 普通类别充电车辆可被挤为等待的三种触发：总上限下降、
	// 优先类别用量增加（更高类别车辆到达/恢复）、自身被调低优先级。
	normalMaySuspend := ctx.cause == causeCapDown || fastUsed > oldFastUsed
	normalPlan := s.allocClass(PriorityNormal, s.cap-fastUsed, normalMaySuspend, ctx.demoted)

	plan := allocPlan{}
	for id, p := range fastPlan {
		plan[id] = p
	}
	for id, p := range normalPlan {
		plan[id] = p
	}
	for _, se := range s.sessions {
		if p, ok := plan[se.ID]; ok {
			se.Pwr = p
			se.State = StateCharging
		} else if se.State != StateFull {
			se.Pwr = 0
			se.State = StateWaiting
		} else {
			se.Pwr = 0
		}
	}
}

// allocClass 计算单个类别在给定预算下的分配。
//
// 候选池 = 原充电车辆（按插枪序）后接原等待车辆（按插枪序）；
// 被调低优先级的车辆按等待车辆入池。每轮水位分摊后从低于最低
// 功率的车辆中挤出一台：先原等待者、后原充电者，同状态取最晚
// 插枪者。maySuspendChargers 为 false 时（受滞回保护的触发）
// 不得挤出原充电车辆，改挤池中最晚插枪的等待车辆；池中已无
// 等待者时说明确无足够功率，只能挤出最晚插枪的充电者。
func (s *Station) allocClass(pri Priority, budget int, maySuspendChargers bool, demoted map[string]bool) allocPlan {
	var chargers, waiters []*sess
	for _, se := range s.sessions {
		if se.State == StateFull || se.Priority != pri {
			continue
		}
		if se.State == StateCharging && !demoted[se.ID] {
			chargers = append(chargers, se)
		} else {
			waiters = append(waiters, se)
		}
	}
	sortByPlug(chargers)
	sortByPlug(waiters)
	pool := make([]*sess, 0, len(chargers)+len(waiters))
	pool = append(pool, chargers...)
	pool = append(pool, waiters...)
	charger := make(map[string]bool, len(chargers))
	for _, se := range chargers {
		charger[se.ID] = true
	}

	removed := map[string]bool{}
	for {
		plan := waterfill(pool, budget, removed)
		victim := pickVictim(pool, removed, charger, plan, maySuspendChargers)
		if victim == "" {
			out := allocPlan{}
			for _, se := range pool {
				if !removed[se.ID] {
					out[se.ID] = plan[se.ID]
				}
			}
			return out
		}
		removed[victim] = true
	}
}

// waterfill 在给定有序车辆间做整数水位分摊：
// 每台不超过自身上限；未封顶者均分剩余，余量按插枪先后每台加一。
func waterfill(order []*sess, budget int, removed map[string]bool) allocPlan {
	alive := make([]*sess, 0, len(order))
	for _, se := range order {
		if !removed[se.ID] {
			alive = append(alive, se)
		}
	}
	if budget < 0 {
		budget = 0
	}
	pw := make(map[string]int, len(alive))
	uncapped := map[string]bool{}
	for _, se := range alive {
		uncapped[se.ID] = true
	}
	for len(uncapped) > 0 {
		used := 0
		var unc []*sess
		for _, se := range alive {
			if uncapped[se.ID] {
				unc = append(unc, se)
			} else {
				used += pw[se.ID]
			}
		}
		sortByPlug(unc)
		rest := budget - used
		if rest < 0 {
			rest = 0
		}
		base := rest / len(unc)
		rem := rest % len(unc)
		level := map[string]int{}
		for _, se := range unc {
			level[se.ID] = base
		}
		for _, se := range unc {
			if rem == 0 {
				break
			}
			level[se.ID]++
			rem--
		}
		anyCapped := false
		for _, se := range unc {
			if level[se.ID] >= se.Cap {
				pw[se.ID] = se.Cap
				delete(uncapped, se.ID)
				anyCapped = true
			} else {
				pw[se.ID] = level[se.ID]
			}
		}
		if !anyCapped {
			break
		}
	}
	out := allocPlan{}
	for _, se := range alive {
		out[se.ID] = pw[se.ID]
	}
	return out
}

// pickVictim 选择本轮被挤出（本轮不分配）的车辆。
// 规则：低于最低功率者中先原等待者、后原充电者，同状态最晚插枪；
// 受滞回保护且仅有充电者不可行时，改挤池中最晚插枪的等待者；
// 池中已无等待者时只能挤出最晚插枪的充电者。
func pickVictim(order []*sess, removed map[string]bool, charger map[string]bool, plan allocPlan, maySuspendChargers bool) string {
	badWaiter := ""
	badCharger := ""
	var bwSeq, bcSeq int64 = -1, -1
	latestWaiter := ""
	var lwSeq int64 = -1
	for _, se := range order {
		if removed[se.ID] {
			continue
		}
		if !charger[se.ID] && se.PlugOrder > lwSeq {
			lwSeq = se.PlugOrder
			latestWaiter = se.ID
		}
		if plan[se.ID] >= se.MinPwr {
			continue
		}
		if charger[se.ID] {
			if se.PlugOrder > bcSeq {
				bcSeq = se.PlugOrder
				badCharger = se.ID
			}
		} else if se.PlugOrder > bwSeq {
			bwSeq = se.PlugOrder
			badWaiter = se.ID
		}
	}
	if badWaiter != "" {
		return badWaiter
	}
	if badCharger != "" {
		if maySuspendChargers {
			return badCharger
		}
		if latestWaiter != "" {
			return latestWaiter
		}
		return badCharger
	}
	return ""
}

func sortByPlug(list []*sess) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j-1].PlugOrder > list[j].PlugOrder; j-- {
			list[j-1], list[j] = list[j], list[j-1]
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
