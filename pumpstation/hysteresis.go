package pumpstation

func hysteresisTarget(level int, previous int, cfg Config, available int) int {
	lower := 0
	for k := range cfg.StartLevels {
		if level >= cfg.StartLevels[k] {
			lower = k + 1
		}
	}

	upper := cfg.PumpCount
	for k := range cfg.StopLevels {
		if level <= cfg.StopLevels[k] {
			upper = k
			break
		}
	}

	target := previous
	if target < lower {
		target = lower
	}
	if target > upper {
		target = upper
	}
	if target > available {
		target = available
	}
	if target < 0 {
		target = 0
	}
	return target
}
