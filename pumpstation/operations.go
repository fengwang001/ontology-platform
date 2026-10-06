package pumpstation

import "fmt"

func (c *Controller) SetFault(now int64, pumpID int, faulted bool) (StateChange, error) {
	if err := validateEventTime(now); err != nil {
		return StateChange{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkClock(now); err != nil {
		return StateChange{}, err
	}
	p := c.pumpByID(pumpID)
	if p == nil {
		return StateChange{}, fmt.Errorf("%w: pump %d", ErrPumpNotFound, pumpID)
	}

	actions := make([]Action, 0)
	reason := ""
	if faulted {
		if p.state == PumpFaulted {
			return StateChange{}, fmt.Errorf("%w: pump %d is already faulted", ErrInvalidState, pumpID)
		}
		c.now = now
		c.hasTime = true
		wasRunning := p.running
		if p.running {
			actions = c.stopPump(p, now, ReasonFault, actions)
		}
		p.state = PumpFaulted
		reason = fmt.Sprintf("pump %d reported fault; it is unavailable immediately", pumpID)
		if wasRunning {
			reason += " and was stopped without minimum-run enforcement"
		}
	} else {
		if p.state != PumpFaulted {
			return StateChange{}, fmt.Errorf("%w: pump %d is not faulted", ErrInvalidState, pumpID)
		}
		c.now = now
		c.hasTime = true
		p.state = PumpAvailable
		p.stoppedAt = now
		p.running = false
		reason = fmt.Sprintf("pump %d recovered; stop timing starts at %d", pumpID, now)
	}

	c.target = minInt(c.target, c.availableCount())
	return c.stateChange(now, p, actions, reason), nil
}

func (c *Controller) SetMaintenance(now int64, pumpID int, maintenance bool) (StateChange, error) {
	if err := validateEventTime(now); err != nil {
		return StateChange{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkClock(now); err != nil {
		return StateChange{}, err
	}
	p := c.pumpByID(pumpID)
	if p == nil {
		return StateChange{}, fmt.Errorf("%w: pump %d", ErrPumpNotFound, pumpID)
	}

	actions := make([]Action, 0)
	reason := ""
	if maintenance {
		if p.state == PumpFaulted {
			return StateChange{}, fmt.Errorf("%w: faulted pump %d cannot enter maintenance", ErrInvalidState, pumpID)
		}
		if p.state == PumpMaintenance {
			return StateChange{}, fmt.Errorf("%w: pump %d is already in maintenance", ErrInvalidState, pumpID)
		}

		c.now = now
		c.hasTime = true
		p.state = PumpMaintenance
		reason = fmt.Sprintf("pump %d entered maintenance and is excluded from available capacity", pumpID)
		if p.running {
			if c.dryLock {
				actions = c.stopPump(p, now, ReasonMaintenance, actions)
				reason += "; dry-run lock is active, so it stopped immediately"
			} else if p.meetsMinimumRun(now, c.cfg.MinimumRunDuration) {
				actions = c.stopPump(p, now, ReasonMaintenanceStop, actions)
				reason += "; it had met minimum run duration and stopped"
			} else {
				remaining := p.startedAt + c.cfg.MinimumRunDuration - now
				reason += fmt.Sprintf("; it remains running until minimum run duration is satisfied, remaining=%ds", remaining)
			}
		}
	} else {
		if p.state == PumpFaulted {
			return StateChange{}, fmt.Errorf("%w: faulted pump %d is not in maintenance", ErrInvalidState, pumpID)
		}
		if p.state != PumpMaintenance {
			return StateChange{}, fmt.Errorf("%w: pump %d is not in maintenance", ErrInvalidState, pumpID)
		}
		c.now = now
		c.hasTime = true
		p.state = PumpAvailable
		reason = fmt.Sprintf("pump %d released from maintenance; it is available again", pumpID)
	}

	c.target = minInt(c.target, c.availableCount())
	return c.stateChange(now, p, actions, reason), nil
}

func (c *Controller) stateChange(now int64, p *pump, actions []Action, reason string) StateChange {
	return StateChange{
		Time:    now,
		PumpID:  p.id,
		State:   p.state,
		Actions: actions,
		Reason:  reason,
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
