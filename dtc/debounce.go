package dtc

// debouncer is the per-DTC, per-ignition-cycle debounce counter.
//
// The counter starts at 0. A failed report moves it up by the rise step
// (never above the fail limit), a passed report moves it down by the
// fall step (never below the pass limit). Reaching the fail limit
// judges the detection as failed, reaching the pass limit judges it as
// passed; in between, the previous judgment is kept.
type debouncer struct {
	riseStep  int
	failLimit int
	fallStep  int
	passLimit int

	value    int
	judgment Judgment
}

func newDebouncer(cfg Config) debouncer {
	return debouncer{
		riseStep:  cfg.DebounceRiseStep,
		failLimit: cfg.DebounceFailLimit,
		fallStep:  cfg.DebounceFallStep,
		passLimit: cfg.DebouncePassLimit,
		judgment:  JudgmentNone,
	}
}

// report applies one monitor result and returns the judgment after the
// report (which may be the unchanged previous judgment).
func (d *debouncer) report(passed bool) Judgment {
	if passed {
		d.value -= d.fallStep
		if d.value < d.passLimit {
			d.value = d.passLimit
		}
		if d.value == d.passLimit {
			d.judgment = JudgmentPass
		}
	} else {
		d.value += d.riseStep
		if d.value > d.failLimit {
			d.value = d.failLimit
		}
		if d.value == d.failLimit {
			d.judgment = JudgmentFail
		}
	}
	return d.judgment
}
