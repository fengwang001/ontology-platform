// Package live 实现对局事件流：事件按 seq 从 1 连续递增追加，
// End 事件追加后流关闭。
package live

import (
	"errors"
	"sort"
	"sync"
)

// Kind 是事件类型。
type Kind int

const (
	Normal Kind = iota // 普通事件
	Hidden             // 选手私有信息
	End                // 对局结束
)

// Valid 报告 k 是否为合法事件类型。
func (k Kind) Valid() bool { return k == Normal || k == Hidden || k == End }

var (
	// ErrEnded 表示对局已结束，不能再追加事件。
	ErrEnded = errors.New("live: match already ended")
	// ErrInvalidKind 表示事件类型非法。
	ErrInvalidKind = errors.New("live: invalid event kind")
)

// Event 是一条对局事件。Seq 从 1 连续递增，T 为事件时刻（毫秒）。
type Event struct {
	Seq  int64
	T    int64
	Kind Kind
}

// Stream 是只增的事件流，可并发使用。
//
// 除事件本体之外另维护两个覆盖索引：times（各事件时刻，随单调时钟非降）
// 与 hiddenPrefix（Hidden 数量前缀和）。只有经 Event 读取完整事件记录才
// 计入非导出计数器 touched；索引访问不计入。
type Stream struct {
	mu           sync.RWMutex
	events       []Event // events[i] 的 Seq 为 i+1
	times        []int64
	hiddenPrefix []int64 // hiddenPrefix[i] 为前 i 个事件中 Hidden 的数量
	ended        bool
	tEnd         int64
	touched      int64
}

// NewStream 返回空事件流。
func NewStream() *Stream {
	return &Stream{hiddenPrefix: []int64{0}}
}

// Emit 在时刻 now 追加一条类型为 k 的事件并返回它。
// 对局已结束（已追加 End）时报 ErrEnded。
func (s *Stream) Emit(now int64, k Kind) (Event, error) {
	if !k.Valid() {
		return Event{}, ErrInvalidKind
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return Event{}, ErrEnded
	}
	ev := Event{Seq: int64(len(s.events)) + 1, T: now, Kind: k}
	s.events = append(s.events, ev)
	s.times = append(s.times, now)
	h := s.hiddenPrefix[len(s.hiddenPrefix)-1]
	if k == Hidden {
		h++
	}
	s.hiddenPrefix = append(s.hiddenPrefix, h)
	if k == End {
		s.ended = true
		s.tEnd = now
	}
	return ev, nil
}

// Ended 报告对局是否已结束。
func (s *Stream) Ended() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ended
}

// TEnd 返回 End 事件的时刻 tE；未结束时为 0。
func (s *Stream) TEnd() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tEnd
}

// Len 返回事件总数。
func (s *Stream) Len() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.events))
}

// Event 读取 seq 对应的完整事件记录（seq 从 1 开始），并计入 touched。
func (s *Stream) Event(seq int64) Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched++
	return s.events[seq-1]
}

// UpperBound 返回时刻不超过 cutoff 的事件数量，即满足 t <= cutoff 的
// 最大 seq。只访问 times 索引，不读取事件记录。
func (s *Stream) UpperBound(cutoff int64) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(sort.Search(len(s.times), func(i int) bool { return s.times[i] > cutoff }))
}

// HiddenCount 返回 seq 落在 (lo, hi] 内（即 lo < seq <= hi）的 Hidden 事件数。
// 只访问 hiddenPrefix 索引，不读取事件记录。
func (s *Stream) HiddenCount(lo, hi int64) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hiddenPrefix[hi] - s.hiddenPrefix[lo]
}

// Touched 返回经 Event 读取完整事件记录的累计次数。
func (s *Stream) Touched() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.touched
}

// ResetTouched 将 touched 归零，用于按操作统计读取次数。
func (s *Stream) ResetTouched() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched = 0
}
