package qc

import "sync"

type LevelConfig struct {
	Target int64
	SD     int64
}

type AssayConfig struct {
	Low      LevelConfig
	High     LevelConfig
	ValidFor int64
}

type System struct {
	mu      sync.Mutex
	lastNow int64
	assays  map[string]*assayState
	reports map[string]*patientReport
}

type AssayStatus string

const (
	StatusInControl    AssayStatus = "in_control"
	StatusOutOfControl AssayStatus = "out_of_control"
)

type ReportStatus string

const (
	ReportIssued   ReportStatus = "issued"
	ReportPending  ReportStatus = "pending_review"
	ReportReviewed ReportStatus = "reviewed"
)

type assayState struct {
	instrument    string
	assay         string
	lowCfg        levelConfig
	highCfg       levelConfig
	low           levelState
	high          levelState
	validFor      int64
	outOfControl  bool
	recoveryCount int
	hasNonOOC     bool
	lastNonOOC    int64
	runs          []QCRecord
	reports       []*patientReport
	reviewCursor  int
}

type patientReport struct {
	id         string
	instrument string
	assay      string
	issuedAt   int64
	status     ReportStatus
}

func NewSystem() *System {
	return &System{
		assays:  make(map[string]*assayState),
		reports: make(map[string]*patientReport),
	}
}

func (s *System) RegisterAssay(now int64, instrument, assay string, config AssayConfig) error {
	if !validID(instrument) || !validID(assay) || config.Low.SD <= 0 || config.High.SD <= 0 || config.ValidFor < 0 {
		return errorf(ErrInvalidParameter, "invalid assay registration parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	key := assayKey(instrument, assay)
	if _, exists := s.assays[key]; exists {
		return errorf(ErrInvalidParameter, "assay already registered")
	}
	s.assays[key] = &assayState{
		instrument: instrument,
		assay:      assay,
		lowCfg:     levelConfig{target: config.Low.Target, sd: config.Low.SD},
		highCfg:    levelConfig{target: config.High.Target, sd: config.High.SD},
		validFor:   config.ValidFor,
	}
	s.lastNow = now
	return nil
}

func (s *System) RunQC(now int64, instrument, assay string, lowValue, highValue int64) (QCRecord, error) {
	if !validID(instrument) || !validID(assay) {
		return QCRecord{}, errorf(ErrInvalidParameter, "invalid quality control parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return QCRecord{}, err
	}
	state, err := s.assay(instrument, assay)
	if err != nil {
		return QCRecord{}, err
	}
	triggered, outcome := evaluateRun(state, lowValue, highValue)
	record := QCRecord{
		Time:       now,
		Instrument: instrument,
		Assay:      assay,
		Low:        levelResultFor(state.lowCfg, lowValue),
		High:       levelResultFor(state.highCfg, highValue),
		Triggered:  append([]Rule(nil), triggered...),
		Outcome:    outcome,
	}
	state.runs = append(state.runs, record)
	if outcome == OutcomeReject {
		if !state.outOfControl {
			state.outOfControl = true
			markReportsForReview(state, now)
		}
		state.recoveryCount = 0
	} else {
		if state.outOfControl {
			state.recoveryCount++
			if state.recoveryCount == 2 {
				state.outOfControl = false
				state.recoveryCount = 0
				state.hasNonOOC = true
				state.lastNonOOC = now
				state.reviewCursor = len(state.reports)
			}
		} else {
			state.hasNonOOC = true
			state.lastNonOOC = now
			state.reviewCursor = len(state.reports)
		}
	}
	s.lastNow = now
	return record, nil
}

func (s *System) Calibrate(now int64, instrument, assay string) error {
	if !validID(instrument) || !validID(assay) {
		return errorf(ErrInvalidParameter, "invalid calibration parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	state, err := s.assay(instrument, assay)
	if err != nil {
		return err
	}
	state.low = levelState{}
	state.high = levelState{}
	state.outOfControl = false
	state.recoveryCount = 0
	s.lastNow = now
	return nil
}

func (s *System) IssueReport(now int64, instrument, assay, reportID string) error {
	if !validID(instrument) || !validID(assay) || !validID(reportID) {
		return errorf(ErrInvalidParameter, "invalid report parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.reports[reportID]; exists {
		return errorf(ErrInvalidParameter, "report identifier already exists")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	state, err := s.assay(instrument, assay)
	if err != nil {
		return err
	}
	if state.outOfControl {
		return errorf(ErrAssayOutOfControl, "assay is out of control")
	}
	if !state.hasNonOOC {
		return errorf(ErrNeverControlled, "assay has never had an accepted quality control run")
	}
	if now-state.lastNonOOC > state.validFor {
		return errorf(ErrQCExpired, "quality control validity period has expired")
	}
	report := &patientReport{
		id:         reportID,
		instrument: instrument,
		assay:      assay,
		issuedAt:   now,
		status:     ReportIssued,
	}
	s.reports[reportID] = report
	state.reports = append(state.reports, report)
	s.lastNow = now
	return nil
}

func (s *System) ReviewReport(now int64, reportID string) error {
	if !validID(reportID) {
		return errorf(ErrInvalidParameter, "invalid report identifier")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	report, exists := s.reports[reportID]
	if !exists {
		return errorf(ErrNotFound, "report does not exist")
	}
	if report.status != ReportPending {
		return errorf(ErrStatusMismatch, "report is not pending review")
	}
	report.status = ReportReviewed
	s.lastNow = now
	return nil
}

func (s *System) checkClock(now int64) error {
	if now < 0 || now > 1_000_000_000 {
		return errorf(ErrInvalidParameter, "time is outside the supported range")
	}
	if now < s.lastNow {
		return errorf(ErrClockRewound, "now is earlier than the last accepted operation")
	}
	return nil
}

func (s *System) assay(instrument, assay string) (*assayState, error) {
	state, exists := s.assays[assayKey(instrument, assay)]
	if !exists {
		return nil, errorf(ErrNotFound, "instrument or assay is not registered")
	}
	return state, nil
}

func markReportsForReview(state *assayState, runTime int64) {
	for _, report := range state.reports[state.reviewCursor:] {
		if (!state.hasNonOOC || report.issuedAt > state.lastNonOOC) && report.issuedAt <= runTime {
			report.status = ReportPending
		}
	}
}

func (s *System) AssaySnapshot(instrument, assay string) (AssayStatus, int, int64, error) {
	if !validID(instrument) || !validID(assay) {
		return "", 0, 0, errorf(ErrInvalidParameter, "invalid assay parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.assay(instrument, assay)
	if err != nil {
		return "", 0, 0, err
	}
	status := StatusInControl
	if state.outOfControl {
		status = StatusOutOfControl
	}
	lastNonOOC := int64(-1)
	if state.hasNonOOC {
		lastNonOOC = state.lastNonOOC
	}
	return status, len(state.runs), lastNonOOC, nil
}

func (s *System) LastRun(instrument, assay string) (QCRecord, error) {
	if !validID(instrument) || !validID(assay) {
		return QCRecord{}, errorf(ErrInvalidParameter, "invalid assay parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.assay(instrument, assay)
	if err != nil {
		return QCRecord{}, err
	}
	if len(state.runs) == 0 {
		return QCRecord{}, errorf(ErrNotFound, "assay has no quality control runs")
	}
	return state.runs[len(state.runs)-1], nil
}

func (s *System) ReportStatus(reportID string) (ReportStatus, error) {
	if !validID(reportID) {
		return "", errorf(ErrInvalidParameter, "invalid report identifier")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	report, exists := s.reports[reportID]
	if !exists {
		return "", errorf(ErrNotFound, "report does not exist")
	}
	return report.status, nil
}

func levelResultFor(config levelConfig, value int64) LevelResult {
	return LevelResult{Value: value, Target: config.target, SD: config.sd, Deviation: value - config.target}
}

func validID(value string) bool {
	return value != ""
}

func assayKey(instrument, assay string) string {
	return instrument + "\x00" + assay
}
