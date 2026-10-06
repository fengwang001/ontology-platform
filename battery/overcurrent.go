package battery

// overcurrentTracker watches the absolute sample current against the current
// allowed limit of its own direction (charging current compared with the
// charge limit, discharging magnitude with the discharge limit). A run starts
// at the first strictly-exceeding sample; every following sample is classified
// into one of the three severity tiers and the run latches once its elapsed
// duration reaches that tier's tolerance. Dropping back to the limit resets
// the run, so crossing tiers mid-run is fully supported.
type overcurrentTracker struct {
	cfg     *Config
	running bool
	startMS int64
}

func newOvercurrentTracker(cfg *Config) *overcurrentTracker { return &overcurrentTracker{cfg: cfg} }

func (o *overcurrentTracker) update(s Sample, chargeLimitMA, dischargeLimitMA int64) (latched bool) {
	var limit, magnitude int64
	switch {
	case s.CurrentMA > 0:
		limit, magnitude = chargeLimitMA, s.CurrentMA
	case s.CurrentMA < 0:
		limit, magnitude = dischargeLimitMA, -s.CurrentMA
	default:
		limit, magnitude = 0, 0
	}

	excess := magnitude - limit
	if magnitude <= limit || excess <= 0 {
		o.running = false
		return false
	}
	if !o.running {
		o.running = true
		o.startMS = s.TimeMS
	}
	tier := o.cfg.CurrentTiers[0]
	for _, t := range o.cfg.CurrentTiers {
		if excess > t.Excess {
			tier = t
		}
	}
	return s.TimeMS-o.startMS >= tier.ToleranceMS
}

func (o *overcurrentTracker) resetSustain() { o.running = false }
