package pumpstation

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	pumps := make([]PumpSnapshot, len(c.pumps))
	now := c.now
	for i := range c.pumps {
		source := c.pumps[i]
		pumps[i] = PumpSnapshot{
			ID:             source.id,
			State:          source.state,
			Running:        source.running,
			CumulativeTime: source.totalRuntime(now),
			StartedAt:      source.startedAt,
			StoppedAt:      source.stoppedAt,
			LastStartedAt:  source.lastStartedAt,
		}
	}

	return Snapshot{
		Time:           c.now,
		Level:          c.level,
		LevelKnown:     c.levelKnown,
		Target:         c.target,
		DryLock:        c.dryLock,
		AvailablePumps: c.availableCount(),
		RunningPumps:   c.runningCount(),
		Pumps:          pumps,
	}
}
