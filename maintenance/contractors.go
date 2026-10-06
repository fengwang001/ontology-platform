package maintenance

// betterContractor 按固定次序判断 c 是否优于当前最佳 d：
// 在手数少者优先；相同则最近完成更早者优先（从未完成者最优先，序号 0）；
// 再同则登记序号小者优先。
func (s *Service) betterContractor(c, d *contractor) bool {
	if lc, ld := len(c.holds), len(d.holds); lc != ld {
		return lc < ld
	}
	if c.completedBefore != d.completedBefore {
		return !c.completedBefore
	}
	if c.completedBefore && c.lastCompletion != d.lastCompletion {
		return c.lastCompletion < d.lastCompletion
	}
	return c.id < d.id
}

// eligibleNormal 判断承包商能否作为普通候选承接工单 o。
func (s *Service) eligibleNormal(c *contractor, o *order) bool {
	if !c.active || len(c.holds) >= c.capacity {
		return false
	}
	if _, ok := c.trades[o.trade]; !ok {
		return false
	}
	if _, ok := c.buildings[o.building]; !ok {
		return false
	}
	if o.level == LevelEmergency && !c.acceptsEmergency {
		return false
	}
	if _, rejected := o.rejectedBy[c.id]; rejected {
		return false
	}
	return true
}

// selectContractor 在满足工种/楼栋/容量（紧急还要接受紧急）且未拒过该单的
// 承包商中，按在手数、最近完成时刻、登记序号的固定次序选出最优者。
// 开销只依赖承包商数量，与工单总数无关。
func (s *Service) selectContractor(o *order) *contractor {
	var best *contractor
	for _, cid := range s.contractorIDs {
		c := s.contractors[cid]
		if !s.eligibleNormal(c, o) {
			continue
		}
		if best == nil || s.betterContractor(c, best) {
			best = c
		}
	}
	return best
}

// preemptVictim 在承包商 c 的“未确认在手工单”(pending) 中找出受害单：
// 非紧急单里派单最晚者；同时刻取序号大者（后派者）。
// pending 的大小不超过 c.capacity，且已确认单根本不在其中，
// 故扫描成本与系统工单总数无关。
func preemptVictim(c *contractor, orders map[int]*order) *order {
	var victim *order
	for oid := range c.pending {
		o := orders[oid]
		if o.level == LevelEmergency {
			continue
		}
		if victim == nil || o.dispatchedAt > victim.dispatchedAt ||
			(o.dispatchedAt == victim.dispatchedAt && o.id > victim.id) {
			victim = o
		}
	}
	return victim
}

// selectPreemptor 为紧急单寻找抢占目标承包商。
func (s *Service) selectPreemptor(o *order) *contractor {
	var best *contractor
	for _, cid := range s.contractorIDs {
		c := s.contractors[cid]
		if !c.active || len(c.holds) < c.capacity || !c.acceptsEmergency {
			continue
		}
		if _, ok := c.trades[o.trade]; !ok {
			continue
		}
		if _, ok := c.buildings[o.building]; !ok {
			continue
		}
		if _, rejected := o.rejectedBy[c.id]; rejected {
			continue
		}
		if preemptVictim(c, s.orders) == nil {
			continue
		}
		if best == nil || s.betterContractor(c, best) {
			best = c
		}
	}
	return best
}
