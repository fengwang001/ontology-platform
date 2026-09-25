// Package idle 维护单条事件流的水位状态：当前水位、lastEventPT、
// 迟到丢弃计数与推进规则。依赖 wtm，不依赖 api。
package idle

import (
	"math"
	"sync"

	"ontology/wtm"
)

// Event 是一条被接受（未迟到）的数据事件。
type Event struct {
	Key string
	TS  int64
}

// Stream 是单条流的全部进程内状态，零值不可直接用，须用 NewStream。
type Stream struct {
	mu          sync.Mutex
	delay       int64
	timeout     int64
	wm          int64
	lastEventPT int64
	hasEvent    bool
	dropped     int64
	accepted    []Event
	// lateChecks 记录「判断一个事件是否迟到」时检查过的值个数，
	// 非导出，不出现在任何公开接口；迟到判定只比当前水位，恒为 1。
	lateChecks int64
}

// NewStream 创建流；delay、timeout 须为正（由调用方保证）。
func NewStream(delay, timeout int64) *Stream {
	return &Stream{delay: delay, timeout: timeout, wm: math.MinInt64}
}

// Feed 处理数据事件：迟到则丢弃并计数，否则推进水位并记录事件。
func (s *Stream) Feed(key string, ts, pt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEventPT = pt
	s.hasEvent = true
	s.lateChecks++ // 只比较 ts-delay 与当前水位这一个值，不回扫历史
	if wtm.Late(ts, s.delay, s.wm) {
		s.dropped++
		return
	}
	s.wm = wtm.Advance(s.wm, ts-s.delay)
	s.accepted = append(s.accepted, Event{Key: key, TS: ts})
}

// Tick 推进处理时钟：空闲触发时用 pt-delay 推进水位，否则无操作。
func (s *Stream) Tick(pt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wtm.Idle(pt, s.lastEventPT, s.timeout, s.hasEvent) {
		s.wm = wtm.Advance(s.wm, pt-s.delay)
	}
}

// Watermark 返回当前水位。
func (s *Stream) Watermark() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wm
}

// Dropped 返回迟到丢弃计数。
func (s *Stream) Dropped() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// Accepted 返回已接受事件的副本。
func (s *Stream) Accepted() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.accepted))
	copy(out, s.accepted)
	return out
}
