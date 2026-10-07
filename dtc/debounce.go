package dtc

// Judgment 为去抖判定结果。
type Judgment int

const (
	JudgmentNone Judgment = iota
	JudgmentPass
	JudgmentFail
)

func (j Judgment) String() string {
	switch j {
	case JudgmentPass:
		return "pass"
	case JudgmentFail:
		return "fail"
	}
	return "none"
}

// debouncer 维护单个故障码在一个点火循环内的去抖值与判定。
type debouncer struct {
	riseStep  int
	fallStep  int
	failLimit int
	passLimit int

	value    int
	judgment Judgment
}

func newDebouncer(cfg Config) *debouncer {
	return &debouncer{
		riseStep:  cfg.DebounceRiseStep,
		fallStep:  cfg.DebounceFallStep,
		failLimit: cfg.DebounceFailLimit,
		passLimit: cfg.DebouncePassLimit,
	}
}

// report 处理一次监测上报，返回更新后的判定。
func (d *debouncer) report(passed bool) Judgment {
	if passed {
		d.value -= d.fallStep
		if d.value < d.passLimit {
			d.value = d.passLimit
		}
	} else {
		d.value += d.riseStep
		if d.value > d.failLimit {
			d.value = d.failLimit
		}
	}
	switch {
	case d.value >= d.failLimit:
		d.judgment = JudgmentFail
	case d.value <= d.passLimit:
		d.judgment = JudgmentPass
	}
	return d.judgment
}

// reset 在点火关时将去抖值与判定清零。
func (d *debouncer) reset() {
	d.value = 0
	d.judgment = JudgmentNone
}
