package qc

type naiveAssay struct {
	spec               AssaySpec
	lowValues          []int64
	highValues         []int64
	outOfControl       bool
	hasRun             bool
	hasNonFailureRun   bool
	lastNonFailureTime int64
	consecutiveNormal  int
	reports            []*Report
	reviewAnchor       int
}

type naiveSystem struct {
	clock   int64
	assays  map[assayKey]*naiveAssay
	reports map[string]*Report
}

func newNaiveSystem() *naiveSystem {
	return &naiveSystem{assays: make(map[assayKey]*naiveAssay), reports: make(map[string]*Report)}
}

func naiveAbsExceeds(value, target, sd, multiplier int64) bool {
	deviation := value - target
	limit := sd * multiplier
	return deviation > limit || deviation < -limit
}

func naiveSide(value int64) int {
	if value > 0 {
		return 1
	}
	if value < 0 {
		return -1
	}
	return 0
}

func naiveLevelRules(values []int64, spec LevelSpec) (map[int]bool, []int64) {
	rules := make(map[int]bool)
	deviations := make([]int64, len(values))
	for index, value := range values {
		deviations[index] = value - spec.Target
	}
	if len(deviations) > 0 && naiveAbsExceeds(values[len(values)-1], spec.Target, spec.StandardDeviation, 3) {
		rules[1] = true
	}
	if len(deviations) >= 2 {
		current := deviations[len(deviations)-1]
		previous := deviations[len(deviations)-2]
		if naiveSide(current) != 0 && naiveSide(current) == naiveSide(previous) &&
			naiveAbsExceeds(values[len(values)-1], spec.Target, spec.StandardDeviation, 2) &&
			naiveAbsExceeds(values[len(values)-2], spec.Target, spec.StandardDeviation, 2) {
			rules[2] = true
		}
	}
	if len(deviations) >= 4 {
		side := naiveSide(deviations[len(deviations)-1])
		matched := side != 0 && naiveAbsExceeds(values[len(values)-1], spec.Target, spec.StandardDeviation, 1)
		for offset := 1; offset <= 3 && matched; offset++ {
			deviation := deviations[len(deviations)-1-offset]
			value := values[len(values)-1-offset]
			if naiveSide(deviation) != side || !naiveAbsExceeds(value, spec.Target, spec.StandardDeviation, 1) {
				matched = false
			}
		}
		if matched {
			rules[4] = true
		}
	}
	if len(deviations) >= 10 {
		side := naiveSide(deviations[len(deviations)-1])
		matched := side != 0
		for offset := 1; offset <= 9 && matched; offset++ {
			if naiveSide(deviations[len(deviations)-1-offset]) != side {
				matched = false
			}
		}
		if matched {
			rules[5] = true
		}
	}
	return rules, deviations
}

func (n *naiveSystem) registerAssay(now int64, spec AssaySpec) error {
	if !validSpec(spec) || now < 0 {
		return ErrInvalidArgument
	}
	key := makeAssayKey(spec.InstrumentID, spec.AssayID)
	if _, exists := n.assays[key]; exists {
		return ErrInvalidArgument
	}
	if now < n.clock {
		return ErrClockRollback
	}
	n.assays[key] = &naiveAssay{spec: spec}
	n.clock = now
	return nil
}

func (n *naiveSystem) submitRun(now int64, input RunInput) (RunResult, error) {
	if !validRunInput(input) || now < 0 {
		return RunResult{}, ErrInvalidArgument
	}
	if now < n.clock {
		return RunResult{}, ErrClockRollback
	}
	item := n.assays[makeAssayKey(input.InstrumentID, input.AssayID)]
	if item == nil {
		return RunResult{}, ErrNotFound
	}

	item.lowValues = append(item.lowValues, input.LowValue)
	item.highValues = append(item.highValues, input.HighValue)
	lowRules, lowDeviations := naiveLevelRules(item.lowValues, item.spec.Low)
	highRules, highDeviations := naiveLevelRules(item.highValues, item.spec.High)
	rules := make(map[int]bool)
	for rule := range lowRules {
		rules[rule] = true
	}
	for rule := range highRules {
		rules[rule] = true
	}
	lowDeviation := lowDeviations[len(lowDeviations)-1]
	highDeviation := highDeviations[len(highDeviations)-1]
	lowOverTwo := naiveAbsExceeds(input.LowValue, item.spec.Low.Target, item.spec.Low.StandardDeviation, 2)
	highOverTwo := naiveAbsExceeds(input.HighValue, item.spec.High.Target, item.spec.High.StandardDeviation, 2)
	if naiveSide(lowDeviation) != 0 && naiveSide(lowDeviation) == -naiveSide(highDeviation) && lowOverTwo && highOverTwo {
		rules[3] = true
	}

	orderedRules := make([]int, 0, 5)
	for rule := 1; rule <= 5; rule++ {
		if rules[rule] {
			orderedRules = append(orderedRules, rule)
		}
	}
	failure := len(orderedRules) > 0
	warning := !failure && (lowOverTwo || highOverTwo)
	wasOutOfControl := item.outOfControl
	recovered := false

	if failure {
		if !wasOutOfControl {
			for _, report := range item.reports[item.reviewAnchor:] {
				report.Status = ReportPending
			}
			item.outOfControl = true
		}
		item.consecutiveNormal = 0
	} else {
		if wasOutOfControl {
			if warning {
				item.consecutiveNormal = 0
			} else {
				item.consecutiveNormal++
				if item.consecutiveNormal == 2 {
					item.outOfControl = false
					recovered = true
				}
			}
		}
		if !item.outOfControl {
			item.hasNonFailureRun = true
			item.lastNonFailureTime = now
			item.reviewAnchor = len(item.reports)
			if !wasOutOfControl {
				item.consecutiveNormal = 0
			}
		}
	}

	item.hasRun = true
	n.clock = now
	return RunResult{
		Time: now, Low: LevelResult{Deviation: lowDeviation}, High: LevelResult{Deviation: highDeviation},
		Rules: orderedRules, Warning: warning, Outage: item.outOfControl, Recovered: recovered,
	}, nil
}

func (n *naiveSystem) calibrate(now int64, instrumentID, assayID string) error {
	if !validID(instrumentID) || !validID(assayID) || now < 0 {
		return ErrInvalidArgument
	}
	if now < n.clock {
		return ErrClockRollback
	}
	item := n.assays[makeAssayKey(instrumentID, assayID)]
	if item == nil {
		return ErrNotFound
	}
	item.lowValues = nil
	item.highValues = nil
	item.outOfControl = false
	item.hasRun = false
	item.hasNonFailureRun = false
	item.lastNonFailureTime = 0
	item.consecutiveNormal = 0
	item.reviewAnchor = len(item.reports)
	n.clock = now
	return nil
}

func (n *naiveSystem) issueReport(now int64, reportID, instrumentID, assayID string) error {
	if !validID(reportID) || !validID(instrumentID) || !validID(assayID) || now < 0 {
		return ErrInvalidArgument
	}
	if _, exists := n.reports[reviewKey(reportID)]; exists {
		return ErrInvalidArgument
	}
	if now < n.clock {
		return ErrClockRollback
	}
	item := n.assays[makeAssayKey(instrumentID, assayID)]
	if item == nil {
		return ErrNotFound
	}
	if item.outOfControl {
		return ErrOutOfControl
	}
	if !item.hasRun || !item.hasNonFailureRun {
		return ErrNeverRun
	}
	if now-item.lastNonFailureTime > item.spec.Validity {
		return ErrQcExpired
	}
	report := &Report{ID: reportID, InstrumentID: instrumentID, AssayID: assayID, Time: now, Status: ReportIssued}
	n.reports[reviewKey(reportID)] = report
	item.reports = append(item.reports, report)
	if now == item.lastNonFailureTime {
		item.reviewAnchor = len(item.reports)
	}
	n.clock = now
	return nil
}

func (n *naiveSystem) reviewReport(now int64, reportID string) (Report, error) {
	if !validID(reportID) || now < 0 {
		return Report{}, ErrInvalidArgument
	}
	if now < n.clock {
		return Report{}, ErrClockRollback
	}
	report := n.reports[reviewKey(reportID)]
	if report == nil {
		return Report{}, ErrNotFound
	}
	if report.Status != ReportPending {
		return Report{}, ErrInvalidState
	}
	report.Status = ReportReviewed
	n.clock = now
	return *report, nil
}

func reviewKey(reportID string) string { return reportID }
