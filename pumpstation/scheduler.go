package pumpstation

func (c *Controller) chooseStart(now int64) *pump {
	var best *pump
	for i := range c.pumps {
		p := &c.pumps[i]
		if p.state != PumpAvailable || p.running || !p.meetsMinimumStop(now, c.cfg.MinimumStopDuration) {
			continue
		}
		if c.hasLastStart && now-c.lastStartAt < c.cfg.MinimumStartInterval {
			continue
		}
		if best == nil || p.totalRuntime(now) < best.totalRuntime(now) {
			best = p
		}
	}
	return best
}

func (c *Controller) chooseStop(now int64) *pump {
	var best *pump
	for i := range c.pumps {
		p := &c.pumps[i]
		if p.state != PumpAvailable || !p.running || !p.meetsMinimumRun(now, c.cfg.MinimumRunDuration) {
			continue
		}
		if best == nil || p.totalRuntime(now) >= best.totalRuntime(now) {
			best = p
		}
	}
	return best
}

func (c *Controller) chooseMaintenanceStop(now int64) *pump {
	for i := range c.pumps {
		p := &c.pumps[i]
		if p.state == PumpMaintenance && p.running && p.meetsMinimumRun(now, c.cfg.MinimumRunDuration) {
			return p
		}
	}
	return nil
}

func (c *Controller) availableCount() int {
	count := 0
	for i := range c.pumps {
		if c.pumps[i].state == PumpAvailable {
			count++
		}
	}
	return count
}

func (c *Controller) runningCount() int {
	count := 0
	for i := range c.pumps {
		if c.pumps[i].running {
			count++
		}
	}
	return count
}

func (c *Controller) runningIDs() []int {
	ids := make([]int, 0, len(c.pumps))
	for i := range c.pumps {
		if c.pumps[i].running {
			ids = append(ids, c.pumps[i].id)
		}
	}
	return ids
}

func (c *Controller) startPump(p *pump, now int64, reason ActionReason, actions []Action) []Action {
	p.start(now)
	c.hasLastStart = true
	c.lastStartAt = now
	return append(actions, Action{PumpID: p.id, Kind: ActionStart, Reason: reason})
}

func (c *Controller) stopPump(p *pump, now int64, reason ActionReason, actions []Action) []Action {
	p.stop(now)
	return append(actions, Action{PumpID: p.id, Kind: ActionStop, Reason: reason})
}

func (c *Controller) pumpByID(pumpID int) *pump {
	if pumpID < 1 || pumpID > len(c.pumps) {
		return nil
	}
	return &c.pumps[pumpID-1]
}
