// Package spectate 实现观战者准入、游标与拉取。
//
// Service 组合 live（事件流）与 delay（截止时刻计算），并维护全局时钟、
// 准入模式、好友表与各观战者游标。所有方法由单一互斥锁串行化，
// 并发调用等价于某个串行顺序；相同操作序列重放结果相同。
package spectate

import (
	"errors"
	"sync"

	"ontology/delay"
	"ontology/live"
)

// Mode 是观战准入模式。
type Mode int

const (
	Public      Mode = iota // 任何人可加入
	FriendsOnly             // 仅好友可加入（非裁判）
	Off                     // 非裁判禁止观战
)

// Valid 报告 m 是否为合法模式。
func (m Mode) Valid() bool { return m == Public || m == FriendsOnly || m == Off }

var (
	ErrInvalidParam    = errors.New("spectate: invalid parameter")
	ErrClockRegression = errors.New("spectate: clock regression")
	ErrAlreadyWatching = errors.New("spectate: already watching")
	ErrModeOff         = errors.New("spectate: spectating is off")
	ErrNotFriend       = errors.New("spectate: not a friend")
	ErrFull            = errors.New("spectate: spectator limit reached")
	ErrNotWatching     = errors.New("spectate: viewer not watching")
)

const (
	maxDelay = int64(1_000_000_000)
	maxLimit = int64(100_000)
	maxNow   = int64(1_000_000_000_000)
	maxPull  = int64(1000)
)

type viewer struct {
	judge  bool
	cursor int64 // 已越过（投递或跳过）的最大 seq
}

// Service 是观战服务门面，可并发使用。
type Service struct {
	mu        sync.Mutex
	st        *live.Stream
	d         int64
	m         int64
	now       int64
	mode      Mode
	friends   map[string]bool
	viewers   map[string]*viewer
	nonJudges int64
}

// New 创建观战服务：观战延迟 d 为 0 到 10^9 毫秒，
// 非裁判观战人数上限 m 为 1 到 10^5。初始模式为 Public。
func New(d, m int64) (*Service, error) {
	if d < 0 || d > maxDelay || m < 1 || m > maxLimit {
		return nil, ErrInvalidParam
	}
	return &Service{
		st:      live.NewStream(),
		d:       d,
		m:       m,
		mode:    Public,
		friends: map[string]bool{},
		viewers: map[string]*viewer{},
	}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// checkClock 校验时钟不回退；被拒绝的操作不推进时钟。
func (s *Service) checkClock(now int64) error {
	if now < s.now {
		return ErrClockRegression
	}
	return nil
}

// Emit 在时刻 now 追加一条事件。End 之后再 Emit 报 live.ErrEnded。
func (s *Service) Emit(now int64, k live.Kind) (live.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || !k.Valid() {
		return live.Event{}, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return live.Event{}, err
	}
	ev, err := s.st.Emit(now, k)
	if err != nil {
		return live.Event{}, err
	}
	s.now = now
	return ev, nil
}

// Befriend 将 viewer 标记为好友，可对尚未加入者设置。
func (s *Service) Befriend(now int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || name == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.friends[name] = true
	s.now = now
	return nil
}

// Unfriend 取消 viewer 的好友标记；FriendsOnly 模式下立即移除该观战者
// （裁判不受模式限制，不被移除）。
func (s *Service) Unfriend(now int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || name == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	delete(s.friends, name)
	if s.mode == FriendsOnly {
		if v, ok := s.viewers[name]; ok && !v.judge {
			s.removeViewer(name, v)
		}
	}
	s.now = now
	return nil
}

// Join 加入观战。拒绝次序：参数非法 > 时钟回退 > 已在观战 >
// 非裁判且模式为 Off > 非裁判、FriendsOnly 且非好友 > 非裁判人数已达 M。
// 裁判不占名额、不受模式限制。游标从 0 开始（从头观战）。
func (s *Service) Join(now int64, name string, judge bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || name == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, ok := s.viewers[name]; ok {
		return ErrAlreadyWatching
	}
	if !judge {
		switch {
		case s.mode == Off:
			return ErrModeOff
		case s.mode == FriendsOnly && !s.friends[name]:
			return ErrNotFriend
		case s.nonJudges >= s.m:
			return ErrFull
		}
	}
	s.viewers[name] = &viewer{judge: judge}
	if !judge {
		s.nonJudges++
	}
	s.now = now
	return nil
}

// Leave 退出观战；对不在观战者报 ErrNotWatching。游标作废。
func (s *Service) Leave(now int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || name == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	v, ok := s.viewers[name]
	if !ok {
		return ErrNotWatching
	}
	s.removeViewer(name, v)
	s.now = now
	return nil
}

// SetMode 切换准入模式。改为 Off 时移除全部非裁判观战者；
// 改为 FriendsOnly 时移除非好友的非裁判观战者。被移除者游标作废。
func (s *Service) SetMode(now int64, mode Mode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || !mode.Valid() {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.mode = mode
	for name, v := range s.viewers {
		if v.judge {
			continue
		}
		if mode == Off || (mode == FriendsOnly && !s.friends[name]) {
			s.removeViewer(name, v)
		}
	}
	s.now = now
	return nil
}

// Pull 从该观战者游标之后按 seq 逐个考察事件：t 大于 cutoff 即停；
// 此刻不可投递的 Hidden 被跳过且游标越过（以后不再补发）；可投递者加入结果。
// 结果满 maxN 条后立即停止，游标停在最后一条投递的事件上。
// More 表示停止后游标之后是否还有此刻可投递的事件。
func (s *Service) Pull(now int64, name string, maxN int64) ([]live.Event, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || name == "" || maxN < 1 || maxN > maxPull {
		return nil, false, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, false, err
	}
	v, ok := s.viewers[name]
	if !ok {
		return nil, false, ErrNotWatching
	}
	cutoff, hiddenOK := s.visibility(now, v.judge)
	idx := s.st.UpperBound(cutoff) // 候选为 (cursor, idx]，不读事件记录
	out := []live.Event{}
	for seq := v.cursor + 1; seq <= idx; seq++ {
		ev := s.st.Event(seq)
		if ev.Kind == live.Hidden && !hiddenOK {
			v.cursor = seq // 跳过且不补发
			continue
		}
		out = append(out, ev)
		v.cursor = seq
		if int64(len(out)) == maxN {
			break // 截断即停，不再向后跳过
		}
	}
	more := s.deliverable(v.cursor, idx, hiddenOK) > 0
	s.now = now
	return out, more, nil
}

// Lag 返回游标之后此刻可投递的事件数，不移动游标。
// 完全由索引计算，不读取事件记录（touched 不随事件总数增长）。
func (s *Service) Lag(now int64, name string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || name == "" {
		return 0, ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	v, ok := s.viewers[name]
	if !ok {
		return 0, ErrNotWatching
	}
	cutoff, hiddenOK := s.visibility(now, v.judge)
	idx := s.st.UpperBound(cutoff)
	n := s.deliverable(v.cursor, idx, hiddenOK)
	s.now = now
	return n, nil
}

// visibility 计算该观战者此刻的 cutoff 与 Hidden 是否可投递。
func (s *Service) visibility(now int64, judge bool) (cutoff int64, hiddenOK bool) {
	cutoff = delay.Cutoff(now, s.st.TEnd(), s.d, s.st.Ended(), judge)
	return cutoff, delay.HiddenOK(cutoff, s.st.TEnd(), s.st.Ended(), judge)
}

// deliverable 统计 (cursor, idx] 内此刻可投递的事件数，只访问索引。
func (s *Service) deliverable(cursor, idx int64, hiddenOK bool) int64 {
	total := idx - cursor
	if hiddenOK {
		return total
	}
	return total - s.st.HiddenCount(cursor, idx)
}

func (s *Service) removeViewer(name string, v *viewer) {
	delete(s.viewers, name)
	if !v.judge {
		s.nonJudges--
	}
}

// Touched 返回事件流被读取完整事件记录的累计次数（测试与审计用）。
func (s *Service) Touched() int64 { return s.st.Touched() }

// ResetTouched 将事件流读取计数归零（测试与审计用）。
func (s *Service) ResetTouched() { s.st.ResetTouched() }
