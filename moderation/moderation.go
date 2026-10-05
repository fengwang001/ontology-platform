// Package moderation 是内容审核系统的门面，串联 decision（处罚决定与有效级别）、
// strike（违规计分与账号状态）、appeal（申诉与多人复核）三个包。
//
// 所有操作可并发调用：一把互斥锁把每次操作串行化，结果等价于某个串行顺序。
// 拒绝次序只在门面层判定，且只报第一个错误；被拒绝的操作不改任何状态（含时钟与票数）。
package moderation

import (
	"errors"
	"sync"

	"ontology/appeal"
	"ontology/decision"
	"ontology/strike"
)

// 各类错误可用 errors.Is 区分。
var (
	ErrInvalidParam      = errors.New("参数非法")
	ErrClockRegression   = errors.New("时钟回退")
	ErrContentExists     = errors.New("内容已存在")
	ErrAccountRestricted = errors.New("账号受限")
	ErrDecisionExists    = errors.New("决定已存在")
	ErrContentNotFound   = errors.New("内容不存在")
	ErrAppealExists      = errors.New("申诉已存在")
	ErrDecisionNotFound  = errors.New("决定不存在")
	ErrForbidden         = errors.New("无权申诉")
	ErrAlreadyAppealed   = errors.New("已申诉过")
	ErrAppealExpired     = errors.New("超过期限")
	ErrAppealNotFound    = errors.New("申诉不存在")
	ErrAppealClosed      = errors.New("已结案")
	ErrRecusal           = errors.New("须回避")
	ErrDuplicateVote     = errors.New("重复投票")
)

// State 是创作者账号状态（正常/禁言/封禁），为有效计分之和的纯函数。
type State = strike.State

const (
	Normal = strike.Normal
	Muted  = strike.Muted
	Banned = strike.Banned
)

const (
	maxParam = int64(1_000_000_000) // P、A、Tmax 的上界
	maxNow   = int64(1_000_000_000_000)
)

// System 是内容审核系统。构造参数：计分有效期 P、申诉期限 A、复核时限 Tmax。
type System struct {
	mu     sync.Mutex
	a      int64 // 申诉期限
	maxNow int64 // 已接受操作的最大 now
	dec    *decision.Store
	str    *strike.Ledger
	app    *appeal.Board
}

// New 构造系统，P、A、Tmax 均须在 [1, 1e9]。
func New(p, a, tmax int64) (*System, error) {
	if p < 1 || p > maxParam || a < 1 || a > maxParam || tmax < 1 || tmax > maxParam {
		return nil, ErrInvalidParam
	}
	return &System{
		a:      a,
		maxNow: -1,
		dec:    decision.NewStore(),
		str:    strike.NewLedger(p),
		app:    appeal.NewBoard(tmax),
	}, nil
}

// checkClock 校验 now 合法且不回退。调用方须持锁。
func (s *System) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockRegression
	}
	return nil
}

// Publish 登记一条内容。创作者此刻账号状态不是正常则报账号受限。
// 拒绝次序：参数非法 > 时钟回退 > 内容已存在 > 账号受限。
func (s *System) Publish(now int64, content, creator string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if content == "" || creator == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if s.dec.HasContent(content) {
		return ErrContentExists
	}
	if s.str.State(creator, now) != Normal {
		return ErrAccountRestricted
	}
	s.dec.AddContent(content, creator)
	s.maxNow = now
	return nil
}

// Decide 对内容作出处罚决定，level 为 1（限流）、2（限龄）、3（下架），决定初始生效。
// 拒绝次序：参数非法 > 时钟回退 > 决定已存在 > 内容不存在。
func (s *System) Decide(now int64, id, content string, level int, reviewer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || content == "" || reviewer == "" || level < 1 || level > 3 {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if s.dec.HasDecision(id) {
		return ErrDecisionExists
	}
	c, ok := s.dec.Content(content)
	if !ok {
		return ErrContentNotFound
	}
	s.dec.AddDecision(&decision.Decision{
		ID: id, Content: content, Creator: c.Creator,
		Level: level, Reviewer: reviewer, Now: now,
	})
	s.str.Add(id, c.Creator, now, level-1)
	s.maxNow = now
	return nil
}

// State 是只读查询：返回创作者此刻的账号状态。校验时钟但不推进。
func (s *System) State(creator string, now int64) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if creator == "" {
		return Normal, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return Normal, err
	}
	return s.str.State(creator, now), nil
}

// Level 是只读查询：返回内容的有效级别（生效决定 level 的最大值，无生效决定为 0）。
func (s *System) Level(content string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if content == "" {
		return 0, ErrInvalidParam
	}
	if !s.dec.HasContent(content) {
		return 0, ErrContentNotFound
	}
	return s.dec.EffectiveLevel(content), nil
}

// Appeal 由内容所属创作者对一条决定提出申诉。
// 拒绝次序：参数非法 > 时钟回退 > 申诉已存在 > 决定不存在 > 无权 > 已申诉过 > 超过期限。
func (s *System) Appeal(now int64, appealID, decisionID, by string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if appealID == "" || decisionID == "" || by == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if s.app.Has(appealID) {
		return ErrAppealExists
	}
	d, ok := s.dec.Decision(decisionID)
	if !ok {
		return ErrDecisionNotFound
	}
	if by != d.Creator {
		return ErrForbidden
	}
	if d.Appealed {
		return ErrAlreadyAppealed
	}
	if now > d.Now+s.a {
		return ErrAppealExpired
	}
	s.app.File(&appeal.Appeal{
		ID: appealID, DecisionID: decisionID, By: by,
		OrigReviewer: d.Reviewer, Submitted: now,
	})
	d.Appealed = true
	s.maxNow = now
	return nil
}

// Review 对申诉投一票（overturn 为真投推翻，为假投维持）。先到 2 票同向结案；
// 结案为推翻则决定立即被推翻，计分追溯撤销。
// 拒绝次序：参数非法 > 时钟回退 > 申诉不存在 > 已结案 > 须回避 > 重复投票。
func (s *System) Review(now int64, appealID, reviewer string, overturn bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if appealID == "" || reviewer == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	a, ok := s.app.Get(appealID)
	if !ok {
		return ErrAppealNotFound
	}
	if closed, _ := s.app.Resolution(a, now); closed {
		return ErrAppealClosed
	}
	if reviewer == a.OrigReviewer {
		return ErrRecusal
	}
	if a.HasVoted(reviewer) {
		return ErrDuplicateVote
	}
	resolved, overturned := s.app.AddVote(a, reviewer, overturn)
	if resolved && overturned {
		if d, ok := s.dec.Decision(a.DecisionID); ok {
			d.Overturned = true
		}
		s.str.Remove(a.DecisionID)
	}
	s.maxNow = now
	return nil
}

// AppealStatus 是只读查询：返回申诉在时刻 now 是否已结案及结论
// （overturned 为真表示推翻）。超时未结案视为按维持结案，是 now 的纯函数。
func (s *System) AppealStatus(appealID string, now int64) (closed, overturned bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if appealID == "" {
		return false, false, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return false, false, err
	}
	a, ok := s.app.Get(appealID)
	if !ok {
		return false, false, ErrAppealNotFound
	}
	closed, overturned = s.app.Resolution(a, now)
	return closed, overturned, nil
}
