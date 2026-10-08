package dtc

// cycleEntry holds everything the manager knows about one DTC within
// the current ignition cycle. Entries are created lazily on the first
// monitor report of the cycle, so closing a cycle only touches DTCs
// that actually reported — never the whole history.
type cycleEntry struct {
	deb        debouncer
	hadFail    bool // a failed judgment occurred this cycle
	hadPass    bool // a passed judgment occurred this cycle (monitor completed)
	occCounted bool // the occurrence for this cycle was already counted
}

// cycleContext tracks the data of one ignition cycle: per-DTC entries
// and the warm-up qualification based on environment samples.
type cycleContext struct {
	entries map[string]*cycleEntry

	envSamples int
	minCoolant int
	maxCoolant int
}

func newCycleContext(cfg Config) *cycleContext {
	return &cycleContext{entries: make(map[string]*cycleEntry)}
}

// entry returns the cycle entry for id, creating it on first use.
func (c *cycleContext) entry(cfg Config, id string) *cycleEntry {
	e, ok := c.entries[id]
	if !ok {
		e = &cycleEntry{deb: newDebouncer(cfg)}
		c.entries[id] = e
	}
	return e
}

// observeEnv feeds one environment sample into the warm-up tracker.
func (c *cycleContext) observeEnv(coolant int) {
	if c.envSamples == 0 {
		c.minCoolant, c.maxCoolant = coolant, coolant
	} else {
		if coolant < c.minCoolant {
			c.minCoolant = coolant
		}
		if coolant > c.maxCoolant {
			c.maxCoolant = coolant
		}
	}
	c.envSamples++
}

// isWarmup reports whether the cycle qualifies as a warm-up cycle: the
// coolant temperature rose by at least rise above the cycle minimum and
// reached finalTemp at least once. Only meaningful at ignition off.
func (c *cycleContext) isWarmup(rise, finalTemp int) bool {
	return c.envSamples > 0 &&
		c.maxCoolant-c.minCoolant >= rise &&
		c.maxCoolant >= finalTemp
}
