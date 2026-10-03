package frequency

type testSnapshot struct {
	Records      []exposure
	LastTime     int64
	LastCreative string
	CreativeRun  int
	Day          int64
	Daily        map[string]int64
	Popped       int64
}

func (c *Controller) inspect(user string) testSnapshot {
	c.mu.RLock()
	state := c.users[user]
	c.mu.RUnlock()
	if state == nil {
		return testSnapshot{Day: -1, Daily: map[string]int64{}}
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	records := make([]exposure, len(state.records))
	copy(records, state.records)
	daily := make(map[string]int64, len(state.daily))
	for campaign, count := range state.daily {
		daily[campaign] = count
	}
	return testSnapshot{
		Records:      records,
		LastTime:     state.lastTime,
		LastCreative: state.lastCreative,
		CreativeRun:  state.creativeRun,
		Day:          state.day,
		Daily:        daily,
		Popped:       state.popped,
	}
}
