// Package sampler 以固定间隔在独立协程中采样调用栈并写入调用树。
package sampler

import (
	"sync"
	"time"

	"ontology/stack"
	"ontology/tree"
)

// Clock 是可注入的时间与唤醒源。
type Clock interface {
	Now() time.Time
	// After 在给定间隔后返回一个通道。
	After(d time.Duration) <-chan time.Time
}

// StackSource 是可注入的栈来源。
// busy 为 true 表示该次应采样被丢弃（忙窗口）；ok 为 false 表示取栈失败，
// 按无效样本计且不占应采样次数。
type StackSource interface {
	Stack() (s stack.Stack, busy, ok bool)
}

// Sampler 周期性采样。所有计数在互斥保护下读写，并发查询安全。
type Sampler struct {
	tree    *tree.Tree
	clock   Clock
	source  StackSource
	period  time.Duration
	maxDepth int

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	mu          sync.Mutex
	expected    int64 // 应采样次数（正常间隔的 tick 数）
	dropped     int64 // 忙窗口丢样数
	badInterval int64 // 时钟回拨/非正间隔次数
	lastTick    time.Time
}

// New 创建采样器但不启动。maxDepth<=0 不截断。
func New(t *tree.Tree, c Clock, src StackSource, period time.Duration, maxDepth int) *Sampler {
	return &Sampler{
		tree:     t,
		clock:    c,
		source:   src,
		period:   period,
		maxDepth: maxDepth,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Run 启动采样循环（通常在新协程中调用），直到 Stop。
func (s *Sampler) Run() {
	defer close(s.doneCh)
	s.lastTick = s.clock.Now()
	for {
		select {
		case <-s.stopCh:
			return
		case tick := <-s.clock.After(s.period):
			s.onTick(tick)
		}
	}
}

func (s *Sampler) onTick(tick time.Time) {
	s.mu.Lock()
	gap := tick.Sub(s.lastTick)
	if !tick.After(s.lastTick) || gap <= 0 {
		// 时钟回拨或停滞：跳过该次采样，不占应采样次数。
		s.badInterval++
		s.mu.Unlock()
		return
	}
	s.lastTick = tick
	s.expected++
	s.mu.Unlock()

	got, busy, ok := s.source.Stack()
	switch {
	case !ok:
		// 取栈失败：计为树的无效样本，且抵消本次应采样名额。
		s.tree.Insert(stack.Stack{})
		s.mu.Lock()
		s.expected--
		s.mu.Unlock()
	case busy:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	default:
		s.tree.Insert(stack.Normalize(got.Frames, s.maxDepth))
	}
}

// Stop 幂等停止并等待采样协程退出。
func (s *Sampler) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	<-s.doneCh
}

// Counts 返回应采样次数、丢样数与异常间隔数。
func (s *Sampler) Counts() (expected, dropped, badInterval int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expected, s.dropped, s.badInterval
}
