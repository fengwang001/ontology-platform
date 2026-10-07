package qc

import "sync"

// maxNow 为 now 的合法上界（含），下界为 0。
const maxNow = int64(1_000_000_000)

// System 临床检验室内质控失控判定与报告拦截系统。
// 所有公开方法可并发调用，内部以互斥锁串行化，
// 效果等价于按加锁顺序的某个串行执行；相同操作序列重放结果完全一致。
type System struct {
	mu       sync.Mutex
	projects map[string]*project
	reports  map[int64]*Report
	lastNow  int64
	hasClock bool
}

// New 创建空系统。
func New() *System {
	return &System{
		projects: make(map[string]*project),
		reports:  make(map[int64]*Report),
	}
}

func projectKey(instrument, analyte string) string {
	return instrument + "\x00" + analyte
}

func validNow(now int64) bool {
	return now >= 0 && now <= maxNow
}

// checkClock 校验时钟单调性；被拒绝的操作不得推进时钟。
func (s *System) checkClock(now int64) error {
	if s.hasClock && now < s.lastNow {
		return ErrClockRollback
	}
	return nil
}

func (s *System) accept(now int64) {
	s.lastNow = now
	s.hasClock = true
}

// RegisterAnalyte 登记某台仪器某个项目的质控参数。
// 两个水平各自有靶值（整数）与标准差（正整数），以及质控有效期秒数（正整数）。
// 重复登记同一（仪器，项目）将覆盖参数并清空其运行序列与报告关联。
func (s *System) RegisterAnalyte(instrument, analyte string, lowTarget, lowSD, highTarget, highSD, validity int64) error {
	if instrument == "" || analyte == "" || lowSD <= 0 || highSD <= 0 || validity <= 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[projectKey(instrument, analyte)] = &project{
		low:      levelParam{target: lowTarget, sd: lowSD},
		high:     levelParam{target: highTarget, sd: highSD},
		validity: validity,
		state:    StateInControl,
	}
	return nil
}

// Run 提交一次质控运行：某项目两个水平在同一时刻 now 的测量值。
// 失控运行使项目转为失控并追溯标记报告；失控状态下的运行照常判定记录。
func (s *System) Run(instrument, analyte string, lowValue, highValue, now int64) (RunResult, error) {
	if instrument == "" || analyte == "" || !validNow(now) {
		return RunResult{}, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return RunResult{}, err
	}
	p, ok := s.projects[projectKey(instrument, analyte)]
	if !ok {
		return RunResult{}, ErrNotFound
	}

	res := p.evaluate(lowValue, highValue)
	p.hasRun = true
	if res.Status == RunRejected {
		if p.state == StateInControl {
			p.state = StateOutOfControl
			p.markPendingReviewLocked(s)
		}
		p.cleanStreak = 0
	} else {
		p.lastNonRejTime = now
		p.hasNonRej = true
		// 链上报告的时刻必然不晚于新的最近一次非失控运行时刻，
		// 全部失去被未来失控标记的资格，清空链表。
		p.head = nil
		p.tail = nil
		if p.state == StateOutOfControl {
			if res.Status == RunNormal {
				p.cleanStreak++
				if p.cleanStreak >= 2 {
					p.state = StateInControl
					p.cleanStreak = 0
				}
			} else {
				// 警告运行不计入恢复连续次数，且清零重计。
				p.cleanStreak = 0
			}
		}
	}
	s.accept(now)
	return res, nil
}

// markPendingReviewLocked 把链表上全部报告标记为待复核并清空链表。
// 由链表不变式（见 reportNode 注释），链上节点恰好是
// “上一次非失控运行时刻之后（严格晚于）直至本次失控时刻（含）”
// 出具的全部报告；从无非失控运行时则为全部已出具报告。
// 开销只与待标记报告数相关。
func (p *project) markPendingReviewLocked(s *System) {
	for n := p.head; n != nil; n = n.next {
		if r := s.reports[n.id]; r != nil && r.Status == ReportIssued {
			r.Status = ReportPendingReview
		}
	}
	p.head = nil
	p.tail = nil
}

// Calibrate 校准登记：清空两个水平已有的运行序列（连续性从零计起），
// 失控项目恢复为在控；不改变靶值、标准差与已出具报告的状态。
func (s *System) Calibrate(instrument, analyte string, now int64) error {
	if instrument == "" || analyte == "" || !validNow(now) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	p, ok := s.projects[projectKey(instrument, analyte)]
	if !ok {
		return ErrNotFound
	}
	p.lowSeries = levelSeries{}
	p.highSeries = levelSeries{}
	p.state = StateInControl
	p.cleanStreak = 0
	s.accept(now)
	return nil
}

// IssueReport 在时刻 now 为某项目出具患者报告，返回报告单号。
// 项目失控、从未质控或质控过期（距最近一次非失控运行严格晚于有效期）时拒绝，
// 被拒绝的出具不留任何记录。
func (s *System) IssueReport(instrument, analyte string, now int64) (int64, error) {
	if instrument == "" || analyte == "" || !validNow(now) {
		return 0, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	p, ok := s.projects[projectKey(instrument, analyte)]
	if !ok {
		return 0, ErrNotFound
	}
	if p.state == StateOutOfControl {
		return 0, ErrOutOfControl
	}
	if !p.hasRun {
		return 0, ErrNeverTested
	}
	// 在控且有过运行，则必存在最近一次非失控运行。
	if now-p.lastNonRejTime > p.validity {
		return 0, ErrQCExpired
	}
	id := int64(len(s.reports)) + 1
	s.reports[id] = &Report{ID: id, Analyte: analyte, Time: now, Status: ReportIssued}
	// 只有可能被未来失控追溯标记的报告才入链（不变式见 reportNode）。
	if !p.hasNonRej || now > p.lastNonRejTime {
		node := &reportNode{id: id, time: now}
		if p.tail == nil {
			p.head = node
		} else {
			p.tail.next = node
		}
		p.tail = node
	}
	s.accept(now)
	return id, nil
}

// Review 复核一份待复核报告，将其转为已复核；对其他状态报状态不符。
func (s *System) Review(reportID, now int64) error {
	if reportID <= 0 || !validNow(now) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	r, ok := s.reports[reportID]
	if !ok {
		return ErrNotFound
	}
	if r.Status != ReportPendingReview {
		return ErrBadState
	}
	r.Status = ReportReviewed
	s.accept(now)
	return nil
}

// ReportStatus 查询报告当前状态。
func (s *System) ReportStatus(reportID int64) (ReportStatus, error) {
	if reportID <= 0 {
		return ReportIssued, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reports[reportID]
	if !ok {
		return ReportIssued, ErrNotFound
	}
	return r.Status, nil
}

// ProjectState 查询项目当前状态。
func (s *System) ProjectState(instrument, analyte string) (ProjectState, error) {
	if instrument == "" || analyte == "" {
		return StateInControl, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[projectKey(instrument, analyte)]
	if !ok {
		return StateInControl, ErrNotFound
	}
	return p.state, nil
}
