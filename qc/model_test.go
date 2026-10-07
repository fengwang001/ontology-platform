package qc

// 本文件是被测系统的独立朴素模型：保存全部历史运行与全部报告，
// 直接按题面定义重算，不做任何增量优化。
// 随机对照测试用它与 System 逐步比对，二者结果必须完全一致。

type devPair struct {
	low, high int64
}

type naiveProject struct {
	low, high      levelParam
	validity       int64
	devs           []devPair // 校准后清空
	state          ProjectState
	cleanStreak    int
	lastNonRejTime int64
	hasRun         bool
	hasNonRej      bool
	reportIDs      []int64
}

type naiveModel struct {
	projects map[string]*naiveProject
	reports  map[int64]*Report
	lastNow  int64
	hasClock bool
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		projects: make(map[string]*naiveProject),
		reports:  make(map[int64]*Report),
	}
}

func (m *naiveModel) checkClock(now int64) error {
	if m.hasClock && now < m.lastNow {
		return ErrClockRollback
	}
	return nil
}

func (m *naiveModel) RegisterAnalyte(instrument, analyte string, lowTarget, lowSD, highTarget, highSD, validity int64) error {
	if instrument == "" || analyte == "" || lowSD <= 0 || highSD <= 0 || validity <= 0 {
		return ErrInvalidParam
	}
	m.projects[projectKey(instrument, analyte)] = &naiveProject{
		low:      levelParam{target: lowTarget, sd: lowSD},
		high:     levelParam{target: highTarget, sd: highSD},
		validity: validity,
		state:    StateInControl,
	}
	return nil
}

// trailOver 统计序列末尾（含本次）连续同号且 |dev| > k*sd 的点数。
func trailOver(devs []int64, k, sd int64) int {
	cnt := 0
	for i := len(devs) - 1; i >= 0; i-- {
		d := devs[i]
		if d == 0 || abs(d) <= k*sd {
			break
		}
		if i < len(devs)-1 && sign(d) != sign(devs[i+1]) {
			break
		}
		cnt++
	}
	return cnt
}

// trailSide 统计序列末尾（含本次）连续同号（同侧）的点数，遇零中断。
func trailSide(devs []int64) int {
	cnt := 0
	for i := len(devs) - 1; i >= 0; i-- {
		d := devs[i]
		if d == 0 {
			break
		}
		if i < len(devs)-1 && sign(d) != sign(devs[i+1]) {
			break
		}
		cnt++
	}
	return cnt
}

func (np *naiveProject) evaluate(lowValue, highValue int64) RunResult {
	lowDev := lowValue - np.low.target
	highDev := highValue - np.high.target
	lows := make([]int64, 0, len(np.devs)+1)
	highs := make([]int64, 0, len(np.devs)+1)
	for _, d := range np.devs {
		lows = append(lows, d.low)
		highs = append(highs, d.high)
	}
	lows = append(lows, lowDev)
	highs = append(highs, highDev)

	var rules []RuleID
	if abs(lowDev) > 3*np.low.sd || abs(highDev) > 3*np.high.sd {
		rules = append(rules, Rule1)
	}
	if trailOver(lows, 2, np.low.sd) >= 2 || trailOver(highs, 2, np.high.sd) >= 2 {
		rules = append(rules, Rule2)
	}
	lowOver2 := abs(lowDev) > 2*np.low.sd
	highOver2 := abs(highDev) > 2*np.high.sd
	if lowOver2 && highOver2 && sign(lowDev) != sign(highDev) {
		rules = append(rules, Rule3)
	}
	if trailOver(lows, 1, np.low.sd) >= 4 || trailOver(highs, 1, np.high.sd) >= 4 {
		rules = append(rules, Rule4)
	}
	if trailSide(lows) >= 10 || trailSide(highs) >= 10 {
		rules = append(rules, Rule5)
	}

	status := RunNormal
	if len(rules) > 0 {
		status = RunRejected
	} else if lowOver2 || highOver2 {
		status = RunWarning
	}
	return RunResult{Rules: rules, Status: status}
}

func (m *naiveModel) Run(instrument, analyte string, lowValue, highValue, now int64) (RunResult, error) {
	if instrument == "" || analyte == "" || !validNow(now) {
		return RunResult{}, ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return RunResult{}, err
	}
	np, ok := m.projects[projectKey(instrument, analyte)]
	if !ok {
		return RunResult{}, ErrNotFound
	}
	res := np.evaluate(lowValue, highValue)
	np.devs = append(np.devs, devPair{low: lowValue - np.low.target, high: highValue - np.high.target})
	np.hasRun = true
	if res.Status == RunRejected {
		if np.state == StateInControl {
			np.state = StateOutOfControl
			for _, id := range np.reportIDs {
				r := m.reports[id]
				if r.Status != ReportIssued || r.Time > now {
					continue
				}
				if np.hasNonRej && r.Time <= np.lastNonRejTime {
					continue
				}
				r.Status = ReportPendingReview
			}
		}
		np.cleanStreak = 0
	} else {
		np.lastNonRejTime = now
		np.hasNonRej = true
		if np.state == StateOutOfControl {
			if res.Status == RunNormal {
				np.cleanStreak++
				if np.cleanStreak >= 2 {
					np.state = StateInControl
					np.cleanStreak = 0
				}
			} else {
				np.cleanStreak = 0
			}
		}
	}
	m.lastNow = now
	m.hasClock = true
	return res, nil
}

func (m *naiveModel) Calibrate(instrument, analyte string, now int64) error {
	if instrument == "" || analyte == "" || !validNow(now) {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	np, ok := m.projects[projectKey(instrument, analyte)]
	if !ok {
		return ErrNotFound
	}
	np.devs = np.devs[:0]
	np.state = StateInControl
	np.cleanStreak = 0
	m.lastNow = now
	m.hasClock = true
	return nil
}

func (m *naiveModel) IssueReport(instrument, analyte string, now int64) (int64, error) {
	if instrument == "" || analyte == "" || !validNow(now) {
		return 0, ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	np, ok := m.projects[projectKey(instrument, analyte)]
	if !ok {
		return 0, ErrNotFound
	}
	if np.state == StateOutOfControl {
		return 0, ErrOutOfControl
	}
	if !np.hasRun {
		return 0, ErrNeverTested
	}
	if now-np.lastNonRejTime > np.validity {
		return 0, ErrQCExpired
	}
	id := int64(len(m.reports)) + 1
	m.reports[id] = &Report{ID: id, Analyte: analyte, Time: now, Status: ReportIssued}
	np.reportIDs = append(np.reportIDs, id)
	m.lastNow = now
	m.hasClock = true
	return id, nil
}

func (m *naiveModel) Review(reportID, now int64) error {
	if reportID <= 0 || !validNow(now) {
		return ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	r, ok := m.reports[reportID]
	if !ok {
		return ErrNotFound
	}
	if r.Status != ReportPendingReview {
		return ErrBadState
	}
	r.Status = ReportReviewed
	m.lastNow = now
	m.hasClock = true
	return nil
}

func (m *naiveModel) ReportStatus(reportID int64) (ReportStatus, error) {
	if reportID <= 0 {
		return ReportIssued, ErrInvalidParam
	}
	r, ok := m.reports[reportID]
	if !ok {
		return ReportIssued, ErrNotFound
	}
	return r.Status, nil
}

func (m *naiveModel) ProjectState(instrument, analyte string) (ProjectState, error) {
	if instrument == "" || analyte == "" {
		return StateInControl, ErrInvalidParam
	}
	np, ok := m.projects[projectKey(instrument, analyte)]
	if !ok {
		return StateInControl, ErrNotFound
	}
	return np.state, nil
}
