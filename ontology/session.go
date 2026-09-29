package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Event 表示某一个键（key）在时间点 Ts 发生的一次事件。
// Ts 为单调递增的整数时间戳（单位由调用方约定）。
type Event struct {
	Key string
	Ts  int64
}

// Session 表示同属一个会话的事件集合。
type Session struct {
	Key    string
	Start  int64
	End    int64
	Events []int64
}

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	ErrNonPositiveGap      = errors.New("ontology: gap must be positive")
	ErrEmptyKey            = errors.New("ontology: event key must not be empty")
	ErrKeyCapacityExceeded = errors.New("ontology: distinct key count exceeds capacity")
)

// Splitter 按活动间隙对各键事件做会话切分，支持乱序 / 迟到事件插入。
type Splitter struct {
	mu      sync.RWMutex
	gap     int64
	maxKeys int
	keys    map[string]*keyState
}

type keyState struct {
	// sessions 始终按 Start/End 升序，且相邻会话间满足后段.Start - 前段.End > gap
	// （即不相连）。段内 Events 升序去重。
	sessions []Session
}

// NewSplitter 创建切分器。gap 为活动间隙阈值（必须 > 0），
// maxKeys 为允许出现的不同键数量上限（必须 > 0）。
func NewSplitter(gap, maxKeys int64) (*Splitter, error) {
	if gap <= 0 {
		return nil, ErrNonPositiveGap
	}
	if maxKeys <= 0 {
		return nil, fmt.Errorf("%w: max keys must be positive", ErrKeyCapacityExceeded)
	}
	return &Splitter{
		gap:     gap,
		maxKeys: int(maxKeys),
		keys:    make(map[string]*keyState),
	}, nil
}

// AddEvents 原子地批量加入事件；任一事件非法则整批拒绝，状态不变。
func (s *Splitter) AddEvents(events []Event) (err error) {
	if len(events) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// ---- 先整批校验，任何非法输入都不得改变状态 ----
	seenInBatch := make(map[string]struct{}, len(events))
	for _, e := range events {
		if e.Key == "" {
			return ErrEmptyKey
		}
		seenInBatch[e.Key] = struct{}{}
	}
	newDistinct := 0
	for k := range seenInBatch {
		if _, ok := s.keys[k]; !ok {
			newDistinct++
		}
	}
	if len(s.keys)+newDistinct > s.maxKeys {
		return fmt.Errorf("%w: limit=%d requested=%d",
			ErrKeyCapacityExceeded, s.maxKeys, len(s.keys)+newDistinct)
	}

	// ---- 对受影响键做快照（深拷贝），先在副本上应用，成功后再提交 ----
	snapshot := make(map[string]*keyState, len(seenInBatch))
	for k := range seenInBatch {
		if ks, ok := s.keys[k]; ok {
			snapshot[k] = ks.clone()
		} else {
			snapshot[k] = &keyState{}
		}
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("ontology: panic while adding events: %v", r)
		}
	}()

	for _, e := range events {
		snapshot[e.Key].insert(e.Ts, s.gap)
	}

	for k, ks := range snapshot {
		s.keys[k] = ks
	}
	return nil
}

// Sessions 返回某键当前的全部会话，按开始时间升序；返回切片为副本。
func (s *Splitter) Sessions(key string) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks := s.keys[key]
	if ks == nil {
		return []Session{}
	}
	return ks.cloneSessions(key)
}

// AllSessions 返回所有键的会话（按 Key、Start 排序）；返回切片为深拷贝。
func (s *Splitter) AllSessions() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := 0
	for _, ks := range s.keys {
		total += len(ks.sessions)
	}
	out := make([]Session, 0, total)
	for k, ks := range s.keys {
		out = append(out, ks.cloneSessions(k)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Start < out[j].Start
	})
	return out
}

// SelfCheck 校验内部结构与"按时间排序后批量切分"的参考结果一致。
func (s *Splitter) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for k, ks := range s.keys {
		var ts []int64
		for i, sess := range ks.sessions {
			if sess.Key != "" && sess.Key != k {
				return fmt.Errorf("ontology: self-check: session key mismatch: %q vs %q", sess.Key, k)
			}
			if len(sess.Events) == 0 || sess.Start != sess.Events[0] ||
				sess.End != sess.Events[len(sess.Events)-1] {
				return fmt.Errorf("ontology: self-check: session range mismatch for key %q", k)
			}
			for j := 1; j < len(sess.Events); j++ {
				d := sess.Events[j] - sess.Events[j-1]
				if d <= 0 {
					return fmt.Errorf("ontology: self-check: events not strictly increasing for key %q", k)
				}
				if d > s.gap {
					return fmt.Errorf("ontology: self-check: gap %d inside session for key %q", d, k)
				}
			}
			if i > 0 {
				if ks.sessions[i].Start-ks.sessions[i-1].End <= s.gap {
					return fmt.Errorf("ontology: self-check: adjacent sessions should be merged for key %q", k)
				}
			}
			ts = append(ts, sess.Events...)
		}

		ref, err := SplitSorted(ts, s.gap)
		if err != nil {
			return fmt.Errorf("ontology: self-check: reference split failed for key %q: %w", k, err)
		}
		got := ks.cloneSessions(k)
		for i := range got {
			got[i].Key = ""
		}
		if len(ref) != len(got) {
			return fmt.Errorf("ontology: self-check: session count mismatch for key %q: got %d want %d",
				k, len(got), len(ref))
		}
		for i := range ref {
			if ref[i].Start != got[i].Start || ref[i].End != got[i].End ||
				len(ref[i].Events) != len(got[i].Events) {
				return fmt.Errorf("ontology: self-check: session %d mismatch for key %q", i, k)
			}
			for j := range ref[i].Events {
				if ref[i].Events[j] != got[i].Events[j] {
					return fmt.Errorf("ontology: self-check: session %d event %d mismatch for key %q", i, j, k)
				}
			}
		}
	}
	return nil
}

// insert 将时间戳 ts 插入，按相连关系（|Δt| <= gap）与左右会话合并。
// 不变量：s.sessions 升序、互不相连、段内事件升序去重。
func (st *keyState) insert(ts, gap int64) {
	idx := sort.Search(len(st.sessions), func(i int) bool {
		return st.sessions[i].End >= ts
	})

	// 落在已有会话内部（含相同时刻）：去重后无需结构调整。
	if idx < len(st.sessions) && ts >= st.sessions[idx].Start && ts <= st.sessions[idx].End {
		st.sessions[idx].insertSorted(ts)
		return
	}

	// idx 指向左侧相邻会话（End < ts 中的最后一个），其后为右侧会话（Start > ts）。
	left := -1
	right := len(st.sessions)
	if idx > 0 {
		left = idx - 1
	}
	if idx < len(st.sessions) {
		right = idx
	}

	connectLeft := left >= 0 && ts-st.sessions[left].End <= gap
	connectRight := right < len(st.sessions) && st.sessions[right].Start-ts <= gap

	switch {
	case connectLeft && connectRight:
		// 与左右两侧均相连：把两段及新事件合并为一个会话。
		mergedEvents := make([]int64, 0,
			len(st.sessions[left].Events)+1+len(st.sessions[right].Events))
		mergedEvents = append(mergedEvents, st.sessions[left].Events...)
		mergedEvents = append(mergedEvents, ts)
		mergedEvents = append(mergedEvents, st.sessions[right].Events...)
		merged := Session{
			Start:  st.sessions[left].Start,
			End:    st.sessions[right].End,
			Events: mergedEvents,
		}
		st.sessions[left] = merged
		st.sessions = append(st.sessions[:right], st.sessions[right+1:]...)

	case connectLeft:
		st.sessions[left].Events = append(st.sessions[left].Events, ts)
		st.sessions[left].End = ts

	case connectRight:
		st.sessions[right].Events = append([]int64{ts}, st.sessions[right].Events...)
		st.sessions[right].Start = ts

	default:
		// 两侧都不连：自成新会话，插入到 left 与 right 之间。
		newSession := Session{
			Start:  ts,
			End:    ts,
			Events: []int64{ts},
		}
		pos := left + 1
		st.sessions = append(st.sessions, Session{})
		copy(st.sessions[pos+1:], st.sessions[pos:])
		st.sessions[pos] = newSession
	}
}

// insertSorted 将会话段内时间戳去重插入，保持升序。
func (sess *Session) insertSorted(ts int64) {
	pos := sort.Search(len(sess.Events), func(i int) bool {
		return sess.Events[i] >= ts
	})
	if pos < len(sess.Events) && sess.Events[pos] == ts {
		return
	}
	sess.Events = append(sess.Events, 0)
	copy(sess.Events[pos+1:], sess.Events[pos:])
	sess.Events[pos] = ts
}

func (st *keyState) clone() *keyState {
	cp := &keyState{sessions: make([]Session, len(st.sessions))}
	for i, sess := range st.sessions {
		events := make([]int64, len(sess.Events))
		copy(events, sess.Events)
		cp.sessions[i] = Session{
			Key:    sess.Key,
			Start:  sess.Start,
			End:    sess.End,
			Events: events,
		}
	}
	return cp
}

func (st *keyState) cloneSessions(key string) []Session {
	out := make([]Session, len(st.sessions))
	for i, sess := range st.sessions {
		events := make([]int64, len(sess.Events))
		copy(events, sess.Events)
		out[i] = Session{
			Key:    key,
			Start:  sess.Start,
			End:    sess.End,
			Events: events,
		}
	}
	return out
}
