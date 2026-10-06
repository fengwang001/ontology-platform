package review

import (
	"sync"
	"time"
)

// Service 是职称评审会务服务的全部入口。
//
// 所有修改性操作在同一把互斥锁下串行化，因此并发调用的结果
// 等价于某个串行顺序；相同操作序列重放得到完全相同结果。
type Service struct {
	mu sync.Mutex

	now time.Time // 服务时钟，只许前进

	reviewers  map[int]*Reviewer
	applicants map[int]*applicantState
	reviews    map[int]*reviewState

	occupied map[int]int // 评委编号 -> 占用该评委的在途评审 ID

	nextReviewID int
}

// applicantState 保存申报人相关的回避事实。
type applicantState struct {
	applicant *Applicant
	relations map[int]struct{} // 直接亲属/师生关系（对称登记，双方都写）
	requests  map[int]struct{} // 已受理的回避申请：评委编号集合
	// autoAvoid 是"该申报人以往已作废评审"中任过评委的评委集合。
	// 抽取时只查这一个集合，与历史评审总数无关。
	autoAvoid map[int]struct{}
}

// voteEntry 是一张已投票的内部记录（含作废标记）。
type voteEntry struct {
	reviewer     int
	choice       Choice
	at           time.Time
	panelVersion int
	episode      int // 复议"幕次"：第一轮重开后旧复议票以此隔离
	voided       bool
}

// reviewState 是一次评审的完整内部状态。
type reviewState struct {
	id           int
	applicant    int
	n            int
	minGroups    map[string]int
	status       ReviewStatus
	currentRound int
	version      int

	panel    map[int]int // 评委编号 -> 其加入时的版本号
	versions []*PanelVersion

	votes [2][]voteEntry // 按轮次保存（0=第一轮，1=复议），永不删除，只置 voided

	results []RoundResult

	announcedAt     time.Time
	publicityEnd    time.Time
	objection       bool
	objectionTime   time.Time
	adjudicated     bool
	objectionUpheld bool

	round2Open    bool         // 复议轮是否已开启并接受投票
	round2Episode int          // 当前复议幕次，每次第一轮重开后递增
	tentative     ReviewStatus // 公示中的待生效结论（StatusFinalPass/Fail）
}

// NewService 创建空服务，start 为初始时钟。
func NewService(start time.Time) *Service {
	return &Service{
		now:          start,
		reviewers:    map[int]*Reviewer{},
		applicants:   map[int]*applicantState{},
		reviews:      map[int]*reviewState{},
		occupied:     map[int]int{},
		nextReviewID: 1,
	}
}

// Now 返回服务当前时钟。
func (s *Service) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// checkClock 仅校验时钟单调性，不推进时钟。这样在校验链后段失败时，
// 被拒绝的操作不会改变服务时钟；操作确认成功时由 advanceClock 推进。
func (s *Service) checkClock(at time.Time) error {
	if at.Before(s.now) {
		return errClock("操作时刻 %s 早于服务时钟 %s", formatTime(at), formatTime(s.now))
	}
	return nil
}

// advanceClock 在操作通过全部校验、即将落盘时推进时钟。
func (s *Service) advanceClock(at time.Time) { s.now = at }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
