package pumpstation

import "sync"

type pump struct {
	id             int
	state          PumpState
	running        bool
	cumulativeTime int64
	startedAt      int64
	stoppedAt      int64
	lastStartedAt  int64
}

type Controller struct {
	mu sync.Mutex

	cfg          Config
	pumps        []pump
	now          int64
	hasTime      bool
	level        int
	levelKnown   bool
	target       int
	dryLock      bool
	hasLastStart bool
	lastStartAt  int64
}

func New(cfg Config) (*Controller, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return newController(cfg), nil
}

func newController(cfg Config) *Controller {
	startLevels := append([]int(nil), cfg.StartLevels...)
	stopLevels := append([]int(nil), cfg.StopLevels...)
	cfg.StartLevels = startLevels
	cfg.StopLevels = stopLevels
	pumps := make([]pump, cfg.PumpCount)
	for i := range pumps {
		pumps[i] = pump{
			id:        i + 1,
			state:     PumpAvailable,
			stoppedAt: 0,
		}
	}
	return &Controller{cfg: cfg, pumps: pumps}
}
