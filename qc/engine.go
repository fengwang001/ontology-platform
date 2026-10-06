package qc

import "sync"

type assay struct {
	spec               AssaySpec
	low                levelRuleState
	high               levelRuleState
	outOfControl       bool
	hasRun             bool
	hasNonFailureRun   bool
	lastNonFailureTime int64
	consecutiveNormal  int
	reviewAnchor       int
	reports            []*Report
}

type System struct {
	mu      sync.Mutex
	clock   int64
	assays  map[assayKey]*assay
	reports map[string]*Report
}

func NewSystem() *System {
	return &System{assays: make(map[assayKey]*assay), reports: make(map[string]*Report)}
}

func (s *System) RegisterAssay(now int64, spec AssaySpec) error {
	if !validSpec(spec) || now < 0 {
		return ErrInvalidArgument
	}
	key := makeAssayKey(spec.InstrumentID, spec.AssayID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.assays[key]; exists {
		return ErrInvalidArgument
	}
	if now < s.clock {
		return ErrClockRollback
	}

	s.assays[key] = &assay{spec: spec}
	s.clock = now
	return nil
}

func (s *System) SubmitRun(now int64, input RunInput) (RunResult, error) {
	if !validRunInput(input) || now < 0 {
		return RunResult{}, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.clock {
		return RunResult{}, ErrClockRollback
	}
	item := s.assays[makeAssayKey(input.InstrumentID, input.AssayID)]
	if item == nil {
		return RunResult{}, ErrNotFound
	}

	lowDeviation, lowRules := item.low.evaluate(item.spec.Low, input.LowValue)
	highDeviation, highRules := item.high.evaluate(item.spec.High, input.HighValue)
	triggered := make(map[int]bool, 5)
	for rule := range lowRules {
		triggered[rule] = true
	}
	for rule := range highRules {
		triggered[rule] = true
	}

	lowOverTwo := magnitudeExceeds(input.LowValue, item.spec.Low.Target, item.spec.Low.StandardDeviation, 2)
	highOverTwo := magnitudeExceeds(input.HighValue, item.spec.High.Target, item.spec.High.StandardDeviation, 2)
	if (lowDeviation > 0 && highDeviation < 0 || lowDeviation < 0 && highDeviation > 0) && lowOverTwo && highOverTwo {
		triggered[3] = true
	}

	rules := make([]int, 0, 5)
	for rule := 1; rule <= 5; rule++ {
		if triggered[rule] {
			rules = append(rules, rule)
		}
	}

	failure := len(rules) > 0
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
	s.clock = now
	return RunResult{
		Time: now, Low: LevelResult{Deviation: lowDeviation}, High: LevelResult{Deviation: highDeviation},
		Rules: rules, Warning: warning, Outage: item.outOfControl, Recovered: recovered,
	}, nil
}

func (s *System) Calibrate(now int64, instrumentID, assayID string) error {
	if !validID(instrumentID) || !validID(assayID) || now < 0 {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.clock {
		return ErrClockRollback
	}
	item := s.assays[makeAssayKey(instrumentID, assayID)]
	if item == nil {
		return ErrNotFound
	}

	item.low.reset()
	item.high.reset()
	item.outOfControl = false
	item.hasRun = false
	item.hasNonFailureRun = false
	item.lastNonFailureTime = 0
	item.consecutiveNormal = 0
	item.reviewAnchor = len(item.reports)
	s.clock = now
	return nil
}

func (s *System) IssueReport(now int64, reportID, instrumentID, assayID string) error {
	if !validID(reportID) || !validID(instrumentID) || !validID(assayID) || now < 0 {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.reports[reportID]; exists {
		return ErrInvalidArgument
	}
	if now < s.clock {
		return ErrClockRollback
	}
	item := s.assays[makeAssayKey(instrumentID, assayID)]
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
	s.reports[reportID] = report
	item.reports = append(item.reports, report)
	if now == item.lastNonFailureTime {
		item.reviewAnchor = len(item.reports)
	}
	s.clock = now
	return nil
}

func (s *System) ReviewReport(now int64, reportID string) (Report, error) {
	if !validID(reportID) || now < 0 {
		return Report{}, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.clock {
		return Report{}, ErrClockRollback
	}
	report := s.reports[reportID]
	if report == nil {
		return Report{}, ErrNotFound
	}
	if report.Status != ReportPending {
		return Report{}, ErrInvalidState
	}

	report.Status = ReportReviewed
	s.clock = now
	return *report, nil
}

func (s *System) ReportStatus(reportID string) (ReportStatus, error) {
	if !validID(reportID) {
		return "", ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	report := s.reports[reportID]
	if report == nil {
		return "", ErrNotFound
	}
	return report.Status, nil
}

func validID(value string) bool { return value != "" }

func validSpec(spec AssaySpec) bool {
	return validID(spec.InstrumentID) && validID(spec.AssayID) &&
		spec.Low.StandardDeviation > 0 && spec.High.StandardDeviation > 0 && spec.Validity > 0
}

func validRunInput(input RunInput) bool {
	return validID(input.InstrumentID) && validID(input.AssayID)
}

type assayKey struct {
	instrumentID string
	assayID      string
}

func makeAssayKey(instrumentID, assayID string) assayKey {
	return assayKey{instrumentID: instrumentID, assayID: assayID}
}
