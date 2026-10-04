package spectate

import (
	"sync"

	"ontology/delay"
	"ontology/live"
)

// Mode 是观战准入模式。
type Mode int

const (
	Public Mode = iota
	FriendsOnly
	Off
)

// Event 是对外暴露的对局事件类型。
type Event = live.Event

// PullResult 是一次拉取的结果: Events 为投递事件(seq 严格递增),
// More 报告停止后游标之后是否还有此刻可投递的事件。
type PullResult struct {
	Events []Event
	More   bool
}

// Touch 统计一次读取实际访问的事件记录数与排序索引探针数。
// 索引探针不是完整事件记录; Lag/Pull 的 More 判定只走索引。
type Touch struct {
	Records int
	Probes  int
}

type viewerState struct {
	judge  bool
	cursor int64
}

// Service 组合对局事件流与延迟可见性, 维护准入模式、好友标记、
// 非裁判名额与每个观战者的投递游标。所有方法可并发调用。
type Service struct {
	mu     sync.Mutex
	d      int64
	m      int
	stream *live.Stream
	maxNow int64

	mode     Mode
	friends  map[string]bool
	viewers  map[string]*viewerState
	nonJudge int

	// 排序索引(随 Emit 增量维护): byTime 为按 t 升序的事件 seq;
	// hiddenPrefix[i] = byTime 前 i 项中 Hidden 事件条数。
	byTime       []int64
	hiddenPrefix []int

	lastTouch Touch
}

// New 创建服务: D 为 0..1e9 毫秒, M 为 1..1e5 的非裁判名额。
func New(D int64, M int) (*Service, error) {
	if D < 0 || D > 1_000_000_000 || M < 1 || M > 100_000 {
		return nil, ErrInvalidParam
	}
	return &Service{
		d:            D,
		m:            M,
		stream:       live.NewStream(),
		mode:         Public,
		friends:      map[string]bool{},
		viewers:      map[string]*viewerState{},
		hiddenPrefix: []int{0},
	}, nil
}

func (s *Service) checkNow(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockRollback
	}
	return nil
}

func (s *Service) delayView() delay.View {
	tE, ended := s.stream.EndTime()
	return delay.View{D: s.d, Ended: ended, TE: tE}
}

// Emit 追加对局事件并推进统一时钟。
func (s *Service) Emit(now int64, kind live.Kind) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind != live.Normal && kind != live.Hidden && kind != live.End {
		return Event{}, ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return Event{}, err
	}
	if _, err := s.stream.Emit(now, kind); err != nil {
		switch err {
		case live.ErrAlreadyEnded:
			return Event{}, ErrAlreadyEnded
		default:
			return Event{}, err
		}
	}
	e, _ := s.stream.At(int64(s.stream.Len()))
	s.insertIndex(e)
	s.maxNow = now
	return e, nil
}

// insertIndex 维护排序索引与 Hidden 前缀和。Emit 的 now 单调不减,
// 故按 t 升序的索引就是 seq 顺序, 直接追加即可(O(1))。
func (s *Service) insertIndex(e Event) {
	s.byTime = append(s.byTime, e.Seq)
	last := s.hiddenPrefix[len(s.hiddenPrefix)-1]
	if e.Kind == live.Hidden {
		last++
	}
	s.hiddenPrefix = append(s.hiddenPrefix, last)
}

// countLE 仅经排序索引统计 t<=cutoff 的事件数(不读事件记录)。
func (s *Service) countLE(cutoff int64, touch *Touch) int {
	// 时间随 Emit 单调, 顺序存于 byTime, 用二分定位, 探针计入 Probes。
	lo, hi := 0, len(s.byTime)
	for lo < hi {
		mid := (lo + hi) / 2
		ev, _ := s.stream.At(s.byTime[mid])
		touch.Probes++
		if ev.Now <= cutoff {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// countDeliverable 仅走索引统计 seq>cursor 中此刻可投递的事件数。
func (s *Service) countDeliverable(cursor int64, now int64, st *viewerState, touch *Touch) int {
	v := s.delayView()
	cutoff := delay.Cutoff(now, st.judge, v)
	k := s.countLE(cutoff, touch)
	openHidden := v.Ended && cutoff == v.TE
	// 排序索引按 (t, seq) 升序, 前 k 项内 seq<=cursor 者仍是前缀, 二分定位。
	lo, hi := 0, k
	for lo < hi {
		mid := (lo + hi) / 2
		touch.Probes++
		ev, _ := s.stream.At(s.byTime[mid])
		if ev.Seq <= cursor {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	p := lo
	total := k
	if !st.judge && !openHidden {
		total -= s.hiddenPrefix[k]
	}
	prefix := p
	if !st.judge && !openHidden {
		prefix -= s.hiddenPrefix[p]
	}
	return total - prefix
}

// SetMode 改变准入模式; Off 移除全部非裁判, FriendsOnly 移除非好友非裁判。
func (s *Service) SetMode(now int64, mode Mode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode != Public && mode != FriendsOnly && mode != Off {
		return ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	if mode == s.mode {
		s.maxNow = now
		return nil
	}
	for name, st := range s.viewers {
		if st.judge {
			continue
		}
		if mode == Off || (mode == FriendsOnly && !s.friends[name]) {
			delete(s.viewers, name)
			s.nonJudge--
		}
	}
	s.mode = mode
	s.maxNow = now
	return nil
}

// Befriend 标记好友, 可对尚未加入者设置。
func (s *Service) Befriend(now int64, viewer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if viewer == "" {
		return ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	s.friends[viewer] = true
	s.maxNow = now
	return nil
}

// Unfriend 撤销好友; 若其正在以非裁判身份在 FriendsOnly 下观战, 立即移除。
func (s *Service) Unfriend(now int64, viewer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if viewer == "" {
		return ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	delete(s.friends, viewer)
	if st, ok := s.viewers[viewer]; ok && !st.judge && s.mode == FriendsOnly {
		delete(s.viewers, viewer)
		s.nonJudge--
	}
	s.maxNow = now
	return nil
}

// Join 让观战者加入。拒绝次序: 参数非法 > 时钟回退 > 已在观战 >
// 非裁判且 Off > 非裁判 FriendsOnly 非好友 > 非裁判名额满。
func (s *Service) Join(now int64, viewer string, judge bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if viewer == "" {
		return ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	if _, ok := s.viewers[viewer]; ok {
		return ErrAlreadyJoined
	}
	if !judge {
		if s.mode == Off {
			return ErrModeOff
		}
		if s.mode == FriendsOnly && !s.friends[viewer] {
			return ErrNotFriend
		}
		if s.nonJudge >= s.m {
			return ErrSpectatorLimit
		}
	}
	s.viewers[viewer] = &viewerState{judge: judge, cursor: 0}
	if !judge {
		s.nonJudge++
	}
	s.maxNow = now
	return nil
}

// Leave 让观战者离开; 不在观战报不存在。好友标记保留。
func (s *Service) Leave(now int64, viewer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if viewer == "" {
		return ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	st, ok := s.viewers[viewer]
	if !ok {
		return ErrNotSpectating
	}
	delete(s.viewers, viewer)
	if !st.judge {
		s.nonJudge--
	}
	s.maxNow = now
	return nil
}

// Lag 返回游标之后此刻可投递的事件数, 不移动游标, 只走排序索引。
func (s *Service) Lag(now int64, viewer string) (int, Touch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if viewer == "" {
		return 0, Touch{}, ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return 0, Touch{}, err
	}
	st, ok := s.viewers[viewer]
	if !ok {
		return 0, Touch{}, ErrNotSpectating
	}
	touch := Touch{}
	n := s.countDeliverable(st.cursor, now, st, &touch)
	s.lastTouch = touch
	s.maxNow = now
	return n, touch, nil
}

// Pull 拉取至多 maxN 条可投递事件, 语义见包设计。
func (s *Service) Pull(now int64, viewer string, maxN int) (PullResult, Touch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if viewer == "" || maxN < 1 || maxN > 1000 {
		return PullResult{}, Touch{}, ErrInvalidParam
	}
	if err := s.checkNow(now); err != nil {
		return PullResult{}, Touch{}, err
	}
	st, ok := s.viewers[viewer]
	if !ok {
		return PullResult{}, Touch{}, ErrNotSpectating
	}

	v := s.delayView()
	cutoff := delay.Cutoff(now, st.judge, v)
	touch := Touch{}
	res := PullResult{Events: []Event{}}

	stoppedFull := false
	seq := st.cursor + 1
	for {
		e, exists := s.stream.At(seq)
		if !exists {
			break
		}
		touch.Records++
		if e.Now > cutoff {
			break
		}
		deliverable := st.judge || e.Kind != live.Hidden ||
			(v.Ended && cutoff == v.TE)
		if deliverable {
			res.Events = append(res.Events, e)
			st.cursor = seq
			if len(res.Events) >= maxN {
				stoppedFull = true
				break
			}
		} else {
			// 此刻不可投递的 Hidden 被游标越过, 以后不再补发。
			st.cursor = seq
		}
		seq++
	}

	if stoppedFull {
		touch2 := Touch{}
		remain := s.countDeliverable(st.cursor, now, st, &touch2)
		touch.Probes += touch2.Probes
		res.More = remain > 0
	}

	s.lastTouch = touch
	s.maxNow = now
	return res, touch, nil
}

// LastTouch 返回最近一次 Pull/Lag 的读取计数(测试用)。
func (s *Service) LastTouch() Touch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastTouch
}
