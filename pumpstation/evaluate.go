package pumpstation

import (
	"fmt"
	"strings"
)

func (c *Controller) ReportLevel(now int64, level int) (Evaluation, error) {
	if err := validateEventTime(now); err != nil {
		return Evaluation{}, err
	}
	if level < 0 {
		return Evaluation{}, fmt.Errorf("%w: level must be non-negative", ErrInvalidArgument)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.checkClock(now); err != nil {
		return Evaluation{}, err
	}

	c.now = now
	c.hasTime = true
	c.level = level
	c.levelKnown = true
	actions := make([]Action, 0)
	reasonParts := []string{}

	if level <= c.cfg.DryRunLevel {
		c.dryLock = true
		c.target = 0
		reasonParts = append(reasonParts, fmt.Sprintf("level %d <= dry-run line %d: stop every running pump and lock", level, c.cfg.DryRunLevel))
		for i := range c.pumps {
			if c.pumps[i].running {
				actions = c.stopPump(&c.pumps[i], now, ReasonDryRun, actions)
			}
		}
	} else {
		if c.dryLock && level > c.cfg.DryRunRecoveryLevel {
			c.dryLock = false
			c.target = 0
			reasonParts = append(reasonParts, fmt.Sprintf("level %d > dry-run recovery line %d: release lock and reset hysteresis target to 0", level, c.cfg.DryRunRecoveryLevel))
		}

		if !c.dryLock {
			available := c.availableCount()
			c.target = hysteresisTarget(level, c.target, c.cfg, available)
			reasonParts = append(reasonParts, fmt.Sprintf("hysteresis level=%d previous_target=%d available=%d target=%d", level, c.target, available, c.target))

			if level >= c.cfg.OverflowLevel {
				c.target = available
				reasonParts = append(reasonParts, fmt.Sprintf("level %d >= overflow line %d: start every stopped available pump", level, c.cfg.OverflowLevel))
				for i := range c.pumps {
					p := &c.pumps[i]
					if p.state == PumpAvailable && !p.running {
						actions = c.startPump(p, now, ReasonOverflow, actions)
					}
				}
			} else {
				if p := c.chooseMaintenanceStop(now); p != nil {
					actions = c.stopPump(p, now, ReasonMaintenanceStop, actions)
					reasonParts = append(reasonParts, fmt.Sprintf("maintenance pump %d met minimum run duration and stopped", p.id))
				}
				running := c.runningCount()
				if len(actions) == 0 && running < c.target {
					p := c.chooseStart(now)
					if p != nil {
						actions = c.startPump(p, now, ReasonNormalStart, actions)
						reasonParts = append(reasonParts, fmt.Sprintf("running %d < target %d: start shortest-runtime eligible pump %d", running, c.target, p.id))
					} else {
						reasonParts = append(reasonParts, fmt.Sprintf("running %d < target %d: no pump satisfies stop duration and start interval; hold", running, c.target))
					}
				} else if len(actions) == 0 && running > c.target {
					p := c.chooseStop(now)
					if p != nil {
						actions = c.stopPump(p, now, ReasonNormalStop, actions)
						reasonParts = append(reasonParts, fmt.Sprintf("running %d > target %d: stop longest-runtime eligible pump %d", running, c.target, p.id))
					} else {
						reasonParts = append(reasonParts, fmt.Sprintf("running %d > target %d: no running pump satisfies minimum run duration; hold", running, c.target))
					}
				} else {
					reasonParts = append(reasonParts, fmt.Sprintf("running %d matches target %d; hold", running, c.target))
				}
			}
		} else {
			reasonParts = append(reasonParts, fmt.Sprintf("dry-run lock remains until level > %d; current level %d", c.cfg.DryRunRecoveryLevel, level))
		}
	}

	return Evaluation{
		Time:    now,
		Level:   level,
		Target:  c.target,
		DryLock: c.dryLock,
		Running: c.runningIDs(),
		Actions: actions,
		Reason:  strings.Join(reasonParts, "; "),
	}, nil
}

func validateEventTime(now int64) error {
	if now < 0 {
		return fmt.Errorf("%w: time must be non-negative", ErrInvalidArgument)
	}
	return nil
}

func (c *Controller) checkClock(now int64) error {
	if c.hasTime && now <= c.now {
		return fmt.Errorf("%w: time %d must be strictly after %d", ErrTimeRollback, now, c.now)
	}
	return nil
}
