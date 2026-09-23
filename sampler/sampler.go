// Package sampler 按固定间隔采集调用栈并写入 tree。
package sampler

import (
	"sync"
	"time"

	"ontology/stack"
	"ontology/tree"
)

// Clock 是可注入的时间源。
type Clock func() time.Time

// Source 是可注入的栈来源。
type Source func() []stack.Frame

// Sampler 以固定间隔采样。
type Sampler struct {
	interval time.Duration
	maxDepth int
	clock    Clock
	src      Source
	busy     func(time.Time) bool
	tree     *tree.Tree

	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once

	mu          sync.Mutex
	expected    int
	dropped     int
	badInterval int
}

// Config 配置采样器。
type Config struct {
	Interval time.Duration
	MaxDepth int
	Clock    Clock
	Source   Source
	Busy     func(time.Time) bool // 返回 true 表示此刻目标忙，丢样
}

// New 创建采样器。
func New(cfg Config) *Sampler {
	busy := cfg.Busy
	if busy == nil {
		busy = func(time.Time) bool { return false }
	}
	return &Sampler{
		interval: cfg.Interval,
		maxDepth: cfg.MaxDepth,
		clock:    cfg.Clock,
		src:      cfg.Source,
		busy:     busy,
		tree:     tree.New(),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Tree 返回底层聚合树。
func (s *Sampler) Tree() *tree.Tree { return s.tree }

// Run 按外部驱动的“时钟节拍”运行：每个 now 代表一次应采样时刻。
// 调用方（测试或真实 ticker）逐拍喂入时间，直到 close(stopCh)。
func (s *Sampler) Run(ticks <-chan time.Time) {
	defer close(s.doneCh)
	var prev time.Time
	for {
		select {
		case <-s.stopCh:
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			s.tick(now, prev)
			prev = now
		}
	}
}

func (s *Sampler) tick(now, prev time.Time) {
	s.mu.Lock()
	s.expected++
	if !prev.IsZero() {
		d := now.Sub(prev)
		if d <= 0 || d > s.interval*10 {
			s.badInterval++
			s.mu.Unlock()
			return
		}
	}
	busy := s.busy(now)
	s.mu.Unlock()

	if busy {
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	return
	}
	r := stack.Normalize(s.src(), s.maxDepth)
	s.tree.Insert(r)
}

// Stats 返回应采样次数、丢样数、异常间隔数。
func (s *Sampler) Stats() (expected, dropped, bad int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expected, s.dropped, s.badInterval
}

// Stop 幂等停止并等待采样协程退出。
func (s *Sampler) Stop() {
	s.once.Do(func() { close(s.stopCh) })
	<-s.doneCh
}
