// Package appeal 实现申诉与多人复核。
//
// 每条决定只能被申诉一次；申诉待决期间决定照常生效、照常计分。
// 复核投票先有 2 票同向即结案（至多 3 票，1 比 1 后由第三票决定）；
// 结案为推翻则决定变为已推翻，内容有效级别回落到其余生效决定的最大值，
// 计分自始撤销。提交后 now 不小于提交时刻加 Tmax 仍未结案的申诉视为
// 按维持结案，这是 now 的纯函数，已投的票作废。
package appeal

import (
	"errors"

	"ontology/decision"
	"ontology/strike"
)

// 本包哨兵错误（含自底层再导出的公共错误），均可用 errors.Is 区分。
var (
	ErrInvalidArgument  = strike.ErrInvalidArgument
	ErrClockRegression  = strike.ErrClockRegression
	ErrDecisionNotFound = decision.ErrDecisionNotFound
	ErrAppealExists     = errors.New("appeal: appeal already exists")
	ErrAppealNotFound   = errors.New("appeal: appeal not found")
	ErrNotCreator       = errors.New("appeal: not the content creator")
	ErrAlreadyAppealed  = errors.New("appeal: decision already appealed")
	ErrExpired          = errors.New("appeal: appeal deadline exceeded")
	ErrClosed           = errors.New("appeal: appeal already closed")
	ErrRecusal          = errors.New("appeal: original reviewer must recuse")
	ErrDuplicateVote    = errors.New("appeal: duplicate vote")
)

// Outcome 是申诉在某一时刻的结果。
type Outcome int

const (
	OutcomePending    Outcome = iota // 待决
	OutcomeUpheld                    // 维持（含超时按维持结案）
	OutcomeOverturned                // 推翻
)

func (o Outcome) String() string {
	switch o {
	case OutcomeUpheld:
		return "upheld"
	case OutcomeOverturned:
		return "overturned"
	default:
		return "pending"
	}
}

// Vote 是一票复核投票。
type Vote struct {
	Reviewer string
	Overturn bool
}

// Appeal 是一条申诉。
type Appeal struct {
	ID         string
	DecisionID string
	By         string
	SubmitNow  int64
	Votes      []Vote
	Closed     bool // 因投票结案（同向票恰为 2，总票数不超过 3）
	Overturned bool // 投票结案的结果
}

// closedAt 报告申诉在 now 时刻是否已结案：投票结案，或超时按维持结案。
func (a *Appeal) closedAt(now, timeout int64) bool {
	return a.Closed || now >= a.SubmitNow+timeout
}

// System 是申诉复核子系统，嵌入处罚决定子系统构成完整系统。
type System struct {
	*decision.System
	window  int64 // 申诉期限 A
	timeout int64 // 复核时限 Tmax
	appeals map[string]*Appeal
}

// NewSystem 在处罚决定子系统之上创建申诉复核子系统。
// window 为申诉期限 A，timeout 为复核时限 Tmax，均须在 [1, 1e9]。
func NewSystem(sys *decision.System, window, timeout int64) (*System, error) {
	if !strike.ValidParam(window) || !strike.ValidParam(timeout) {
		return nil, ErrInvalidArgument
	}
	return &System{
		System:  sys,
		window:  window,
		timeout: timeout,
		appeals: make(map[string]*Appeal),
	}, nil
}

// New 以计分有效期 P、申诉期限 A、复核时限 Tmax 创建完整系统。
func New(period, window, timeout int64) (*System, error) {
	store, err := strike.NewStore(period)
	if err != nil {
		return nil, err
	}
	return NewSystem(decision.NewSystem(store), window, timeout)
}

// Appeal 由内容所属创作者对一条决定提出申诉。
// 拒绝次序：参数非法 > 时钟回退 > 申诉已存在 > 决定不存在 > 无权 > 已申诉过 > 超过期限。
func (s *System) Appeal(now int64, appealID, decisionID, by []byte) error {
	if !strike.ValidNow(now) || len(appealID) == 0 || len(decisionID) == 0 || len(by) == 0 {
		return ErrInvalidArgument
	}
	s.Lock()
	defer s.Unlock()
	if err := s.CheckClock(now); err != nil {
		return err
	}
	aid := string(appealID)
	if _, ok := s.appeals[aid]; ok {
		return ErrAppealExists
	}
	d := s.DecisionByID(string(decisionID))
	if d == nil {
		return ErrDecisionNotFound
	}
	if s.ContentByID(d.ContentID).Creator != string(by) {
		return ErrNotCreator
	}
	if d.Appealed {
		return ErrAlreadyAppealed
	}
	if now > d.Now+s.window { // 恰等允许
		return ErrExpired
	}
	d.Appealed = true
	s.appeals[aid] = &Appeal{
		ID:         aid,
		DecisionID: d.ID,
		By:         string(by),
		SubmitNow:  now,
	}
	s.AcceptClock(now)
	return nil
}

// Review 对一条申诉投一票。
// 拒绝次序：参数非法 > 时钟回退 > 申诉不存在 > 已结案 > 须回避 > 重复投票。
func (s *System) Review(now int64, appealID, reviewer []byte, overturn bool) error {
	if !strike.ValidNow(now) || len(appealID) == 0 || len(reviewer) == 0 {
		return ErrInvalidArgument
	}
	s.Lock()
	defer s.Unlock()
	if err := s.CheckClock(now); err != nil {
		return err
	}
	a, ok := s.appeals[string(appealID)]
	if !ok {
		return ErrAppealNotFound
	}
	if a.closedAt(now, s.timeout) {
		return ErrClosed
	}
	d := s.DecisionByID(a.DecisionID)
	if d.Reviewer == string(reviewer) {
		return ErrRecusal
	}
	for _, v := range a.Votes {
		if v.Reviewer == string(reviewer) {
			return ErrDuplicateVote
		}
	}
	a.Votes = append(a.Votes, Vote{Reviewer: string(reviewer), Overturn: overturn})
	var over, keep int
	for _, v := range a.Votes {
		if v.Overturn {
			over++
		} else {
			keep++
		}
	}
	switch {
	case over == 2:
		a.Closed, a.Overturned = true, true
		s.OverturnLocked(d.ID)
	case keep == 2:
		a.Closed = true
	}
	s.AcceptClock(now)
	return nil
}

// Outcome 是只读查询：返回申诉在 now 时刻的结果（超时视为维持是 now 的纯函数）。
func (s *System) Outcome(appealID []byte, now int64) (Outcome, error) {
	s.Lock()
	defer s.Unlock()
	a, ok := s.appeals[string(appealID)]
	if !ok {
		return OutcomePending, ErrAppealNotFound
	}
	if a.Closed {
		if a.Overturned {
			return OutcomeOverturned, nil
		}
		return OutcomeUpheld, nil
	}
	if now >= a.SubmitNow+s.timeout {
		return OutcomeUpheld, nil
	}
	return OutcomePending, nil
}
