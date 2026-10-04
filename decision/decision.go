// Package decision 实现处罚决定与内容有效级别。
//
// 内容的有效级别是其全部生效决定 level 的最大值，没有生效决定则为 0，
// 查询时重算，不随计分过期而变化。创作者被封禁不妨碍对其既有内容继续
// 作出决定；Publish 时账号状态不是正常则报账号受限。
package decision

import (
	"errors"

	"ontology/strike"
)

// 本包哨兵错误（含自 strike 再导出的公共错误），均可用 errors.Is 区分。
var (
	ErrInvalidArgument   = strike.ErrInvalidArgument
	ErrClockRegression   = strike.ErrClockRegression
	ErrContentExists     = errors.New("decision: content already exists")
	ErrContentNotFound   = errors.New("decision: content not found")
	ErrDecisionExists    = errors.New("decision: decision already exists")
	ErrDecisionNotFound  = errors.New("decision: decision not found")
	ErrAccountRestricted = errors.New("decision: account restricted")
)

// Content 是一条已登记的内容。
type Content struct {
	ID        string
	Creator   string
	decisions []*Decision
}

// Decision 是一条处罚决定，初始为生效。
type Decision struct {
	ID         string
	ContentID  string
	Reviewer   string
	Level      int // 1 限流，2 限龄，3 下架
	Now        int64
	Overturned bool // 被复核推翻后为 true，自始不计分
	Appealed   bool // 每条决定至多被申诉一次，无论结果如何
}

// System 是处罚决定子系统，内嵌计分存储（共享其锁与时钟）。
type System struct {
	*strike.Store
	contents  map[string]*Content
	decisions map[string]*Decision
}

// NewSystem 在计分存储之上创建处罚决定子系统。
func NewSystem(store *strike.Store) *System {
	return &System{
		Store:     store,
		contents:  make(map[string]*Content),
		decisions: make(map[string]*Decision),
	}
}

// Publish 登记一条内容。拒绝次序：参数非法 > 时钟回退 > 内容已存在 > 账号受限。
func (s *System) Publish(now int64, content, creator []byte) error {
	if !strike.ValidNow(now) || len(content) == 0 || len(creator) == 0 {
		return ErrInvalidArgument
	}
	s.Lock()
	defer s.Unlock()
	if err := s.CheckClock(now); err != nil {
		return err
	}
	cid := string(content)
	if _, ok := s.contents[cid]; ok {
		return ErrContentExists
	}
	if s.StateLocked(string(creator), now) != strike.StateNormal {
		return ErrAccountRestricted
	}
	s.contents[cid] = &Content{ID: cid, Creator: string(creator)}
	s.AcceptClock(now)
	return nil
}

// Decide 对内容作出处罚决定。拒绝次序：参数非法 > 时钟回退 > 决定已存在 > 内容不存在。
func (s *System) Decide(now int64, id, content []byte, level int, reviewer []byte) error {
	if !strike.ValidNow(now) || len(id) == 0 || len(content) == 0 ||
		len(reviewer) == 0 || level < 1 || level > 3 {
		return ErrInvalidArgument
	}
	s.Lock()
	defer s.Unlock()
	if err := s.CheckClock(now); err != nil {
		return err
	}
	did := string(id)
	if _, ok := s.decisions[did]; ok {
		return ErrDecisionExists
	}
	c, ok := s.contents[string(content)]
	if !ok {
		return ErrContentNotFound
	}
	d := &Decision{
		ID:        did,
		ContentID: c.ID,
		Reviewer:  string(reviewer),
		Level:     level,
		Now:       now,
	}
	s.decisions[did] = d
	c.decisions = append(c.decisions, d)
	if weight := level - 1; weight > 0 { // level 1 不记分
		s.AddScore(did, c.Creator, weight, now)
	}
	s.AcceptClock(now)
	return nil
}

// EffectiveLevel 返回内容的有效级别：全部生效决定 level 的最大值，无为 0。
func (s *System) EffectiveLevel(content []byte) (int, error) {
	s.Lock()
	defer s.Unlock()
	c, ok := s.contents[string(content)]
	if !ok {
		return 0, ErrContentNotFound
	}
	level := 0
	for _, d := range c.decisions {
		if !d.Overturned && d.Level > level {
			level = d.Level
		}
	}
	return level, nil
}

// DecisionOverturned 报告决定当前是否已被推翻。
func (s *System) DecisionOverturned(id []byte) (bool, error) {
	s.Lock()
	defer s.Unlock()
	d, ok := s.decisions[string(id)]
	if !ok {
		return false, ErrDecisionNotFound
	}
	return d.Overturned, nil
}

// OverturnLocked 将决定标记为已推翻并撤销其计分；调用方须持有锁。
func (s *System) OverturnLocked(id string) {
	if d, ok := s.decisions[id]; ok {
		d.Overturned = true
		s.Store.Overturn(id)
	}
}

// DecisionByID 返回决定内部表示，不存在为 nil；调用方须持有锁。
func (s *System) DecisionByID(id string) *Decision { return s.decisions[id] }

// ContentByID 返回内容内部表示，不存在为 nil；调用方须持有锁。
func (s *System) ContentByID(id string) *Content { return s.contents[id] }
