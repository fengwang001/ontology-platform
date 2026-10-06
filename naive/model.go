package naive

type ErrorCode string

func (code ErrorCode) Error() string { return string(code) }

const (
	InvalidParameter ErrorCode = "invalid_parameter"
	ClockRewound     ErrorCode = "clock_rewound"
	NotFound         ErrorCode = "not_found"
	OutOfControl     ErrorCode = "assay_out_of_control"
	NeverControlled  ErrorCode = "never_controlled"
	QCExpired        ErrorCode = "qc_expired"
	StatusMismatch   ErrorCode = "status_mismatch"
)

type Rule string

const (
	Rule1 Rule = "rule_1"
	Rule2 Rule = "rule_2"
	Rule3 Rule = "rule_3"
	Rule4 Rule = "rule_4"
	Rule5 Rule = "rule_5"
)

type Outcome string

const (
	Normal  Outcome = "normal"
	Warning Outcome = "warning"
	Reject  Outcome = "out_of_control"
)

type Level struct{ Target, SD int64 }
type Config struct {
	Low, High Level
	ValidFor  int64
}

type Record struct {
	Time                int64
	Instrument, Assay   string
	LowValue, HighValue int64
	Rules               []Rule
	Outcome             Outcome
}

type ReportStatus string

const (
	Issued   ReportStatus = "issued"
	Pending  ReportStatus = "pending_review"
	Reviewed ReportStatus = "reviewed"
)

type report struct {
	id, instrument, assay string
	issuedAt              int64
	status                ReportStatus
}

type assay struct {
	instrument, assay string
	low, high         Level
	validFor          int64
	runs              []Record
	sequenceFrom      int
	reports           []*report
	reviewFrom        int
	outOfControl      bool
	recovery          int
	hasNonOOC         bool
	lastNonOOC        int64
}

type Model struct {
	lastNow int64
	assays  map[string]*assay
	reports map[string]*report
}

func New() *Model {
	return &Model{assays: map[string]*assay{}, reports: map[string]*report{}}
}

func (m *Model) Register(now int64, instrument, item string, config Config) error {
	if invalidBase(instrument, item, now) || config.Low.SD <= 0 || config.High.SD <= 0 || config.ValidFor < 0 {
		return InvalidParameter
	}
	if err := m.checkClockAfterBase(now); err != nil {
		return err
	}
	if _, exists := m.assays[key(instrument, item)]; exists {
		return InvalidParameter
	}
	m.assays[key(instrument, item)] = &assay{instrument: instrument, assay: item, low: config.Low, high: config.High, validFor: config.ValidFor}
	m.lastNow = now
	return nil
}

func (m *Model) RunQC(now int64, instrument, item string, lowValue, highValue int64) (Record, error) {
	if invalidBase(instrument, item, now) {
		return Record{}, InvalidParameter
	}
	if err := m.checkClockAfterBase(now); err != nil {
		return Record{}, err
	}
	state, exists := m.assays[key(instrument, item)]
	if !exists {
		return Record{}, NotFound
	}
	lowDeviation := lowValue - state.low.Target
	highDeviation := highValue - state.high.Target
	record := Record{Time: now, Instrument: instrument, Assay: item, LowValue: lowValue, HighValue: highValue, Rules: m.rules(state, lowDeviation, highDeviation)}
	if len(record.Rules) > 0 {
		record.Outcome = Reject
	} else if over(lowDeviation, 2, state.low.SD) || over(highDeviation, 2, state.high.SD) {
		record.Outcome = Warning
	} else {
		record.Outcome = Normal
	}
	state.runs = append(state.runs, record)
	if record.Outcome == Reject {
		if !state.outOfControl {
			state.outOfControl = true
			for _, patientReport := range state.reports[state.reviewFrom:] {
				if (!state.hasNonOOC || patientReport.issuedAt > state.lastNonOOC) && patientReport.issuedAt <= now {
					patientReport.status = Pending
				}
			}
		}
		state.recovery = 0
	} else if state.outOfControl {
		state.recovery++
		if state.recovery == 2 {
			state.outOfControl = false
			state.recovery = 0
			state.hasNonOOC = true
			state.lastNonOOC = now
			state.reviewFrom = len(state.reports)
		}
	} else {
		state.hasNonOOC = true
		state.lastNonOOC = now
		state.reviewFrom = len(state.reports)
	}
	m.lastNow = now
	return record, nil
}

func (m *Model) Calibrate(now int64, instrument, item string) error {
	if invalidBase(instrument, item, now) {
		return InvalidParameter
	}
	if err := m.checkClockAfterBase(now); err != nil {
		return err
	}
	state, exists := m.assays[key(instrument, item)]
	if !exists {
		return NotFound
	}
	state.sequenceFrom = len(state.runs)
	state.outOfControl = false
	state.recovery = 0
	m.lastNow = now
	return nil
}

func (m *Model) IssueReport(now int64, instrument, item, reportID string) error {
	if invalidBase(instrument, item, now) || !validID(reportID) || m.duplicate(reportID) {
		return InvalidParameter
	}
	if err := m.checkClockAfterBase(now); err != nil {
		return err
	}
	state, exists := m.assays[key(instrument, item)]
	if !exists {
		return NotFound
	}
	if state.outOfControl {
		return OutOfControl
	}
	if !state.hasNonOOC {
		return NeverControlled
	}
	if now-state.lastNonOOC > state.validFor {
		return QCExpired
	}
	patientReport := &report{id: reportID, instrument: instrument, assay: item, issuedAt: now, status: Issued}
	m.reports[reportID] = patientReport
	state.reports = append(state.reports, patientReport)
	m.lastNow = now
	return nil
}

func (m *Model) ReviewReport(now int64, reportID string) error {
	if !validID(reportID) || now < 0 || now > 1_000_000_000 {
		return InvalidParameter
	}
	if err := m.checkClockAfterBase(now); err != nil {
		return err
	}
	patientReport, exists := m.reports[reportID]
	if !exists {
		return NotFound
	}
	if patientReport.status != Pending {
		return StatusMismatch
	}
	patientReport.status = Reviewed
	m.lastNow = now
	return nil
}

func (m *Model) ReportStatus(reportID string) (ReportStatus, bool) {
	patientReport, exists := m.reports[reportID]
	if !exists {
		return "", false
	}
	return patientReport.status, exists
}

func (m *Model) rules(state *assay, lowDeviation, highDeviation int64) []Rule {
	lowHistory := m.deviations(state, true)
	highHistory := m.deviations(state, false)
	rules := make([]Rule, 0, 5)
	if over(lowDeviation, 3, state.low.SD) || over(highDeviation, 3, state.high.SD) {
		rules = append(rules, Rule1)
	}
	if lastSameOver(lowHistory, lowDeviation, 2, state.low.SD) || lastSameOver(highHistory, highDeviation, 2, state.high.SD) {
		rules = append(rules, Rule2)
	}
	if oppositeOverTwo(lowDeviation, highDeviation, state.low.SD, state.high.SD) {
		rules = append(rules, Rule3)
	}
	if consecutiveOver(lowHistory, lowDeviation, 1, state.low.SD) >= 4 || consecutiveOver(highHistory, highDeviation, 1, state.high.SD) >= 4 {
		rules = append(rules, Rule4)
	}
	if consecutiveSide(lowHistory, lowDeviation) >= 10 || consecutiveSide(highHistory, highDeviation) >= 10 {
		rules = append(rules, Rule5)
	}
	return rules
}

func (m *Model) deviations(state *assay, low bool) []int64 {
	values := make([]int64, 0, len(state.runs)-state.sequenceFrom)
	for _, current := range state.runs[state.sequenceFrom:] {
		if low {
			values = append(values, current.LowValue-state.low.Target)
		} else {
			values = append(values, current.HighValue-state.high.Target)
		}
	}
	return values
}

func lastSameOver(history []int64, current int64, multiplier, sd int64) bool {
	if sign(current) == 0 || !over(current, multiplier, sd) || len(history) == 0 {
		return false
	}
	previous := history[len(history)-1]
	return sign(previous) == sign(current) && over(previous, multiplier, sd)
}

func consecutiveOver(history []int64, current int64, multiplier, sd int64) int {
	if sign(current) == 0 || !over(current, multiplier, sd) {
		return 0
	}
	count := 1
	for index := len(history) - 1; index >= 0; index-- {
		if sign(history[index]) != sign(current) || !over(history[index], multiplier, sd) {
			break
		}
		count++
	}
	return count
}

func consecutiveSide(history []int64, current int64) int {
	if sign(current) == 0 {
		return 0
	}
	count := 1
	for index := len(history) - 1; index >= 0; index-- {
		if sign(history[index]) != sign(current) {
			break
		}
		count++
	}
	return count
}

func oppositeOverTwo(low, high, lowSD, highSD int64) bool {
	return (sign(low)*sign(high) < 0) && over(low, 2, lowSD) && over(high, 2, highSD)
}

func (m *Model) duplicate(reportID string) bool {
	_, exists := m.reports[reportID]
	return exists
}

func (m *Model) checkClockAfterBase(now int64) error {
	if now < m.lastNow {
		return ClockRewound
	}
	return nil
}

func invalidBase(instrument, item string, now int64) bool {
	return !validID(instrument) || !validID(item) || now < 0 || now > 1_000_000_000
}

func validID(value string) bool          { return value != "" }
func key(instrument, item string) string { return instrument + "\x00" + item }

func sign(value int64) int {
	if value > 0 {
		return 1
	}
	if value < 0 {
		return -1
	}
	return 0
}

func abs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func over(deviation, multiplier, sd int64) bool {
	var magnitude uint64
	if deviation < 0 {
		magnitude = uint64(-(deviation + 1)) + 1
	} else {
		magnitude = uint64(deviation)
	}
	return magnitude > uint64(multiplier)*uint64(sd)
}
