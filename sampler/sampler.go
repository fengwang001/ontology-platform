// Package sampler 按固定间隔对调用栈来源采样并写入调用树。
// 时钟与栈来源均可注入，支持丢样、时钟回拨检测与幂等停止。
package sampler

import (
	"errors"
	"sync"
	"time"

	"ontology/stack"
	"ontology/tree"
)

// ErrBusy 由栈来源在「忙」窗口内返回，该次采样记为丢样。
var ErrBusy = errors.New("sampler: source busy")

// Source 提供一次调用栈采样。返回 ErrBusy 表示丢样，
// 返回 stack.ErrEmpty 等其它错误表示无效样本。
type Source interface {
	Stack() (stack.Stack, error)
}

// Ticker 抽象节拍来源，测试可注入手动节拍。
type Ticker interface {
	Chan() <-chan time.Time
	Stop()
}

// Stats 是采样器的可读计数器。
type Stats struct {
	Ticks        uint64 // 收到的节拍总数
	Dropped      uint64 // 来源忙导致的丢样数
	BadIntervals uint64 // 时钟回拨（间隔 <= 0）导致的跳样数
	Invalid      uint64 // 无效样本数（如空栈）
}

// Sampler 在 Run 协程中采样，其它协程可并发 Snapshot 查询，
// 查询只会看到完整插入后的树，不会看到半更新状态。
type Sampler struct {
	src      Source
	tk       Ticker
	clock    func() time.Time
	interval time.Duration
	mu       sync.RWMutex
	tree     *tree.Tree
	statMu   sync.Mutex
	stats    Stats
	done     chan struct{}
	stopped  chan struct{}
	once     sync.Once
}

// New 构造采样器；interval 为期望采样间隔，必须为正。
func New(src Source, tk Ticker, clock func() time.Time, interval time.Duration) *Sampler {
	if interval <= 0 {
		interval = time.Millisecond
	}
	return &Sampler{
		src: src, tk: tk, clock: clock, interval: interval,
		tree: tree.New(), done: make(chan struct{}), stopped: make(chan struct{}),
	}
}

// Run 阻塞运行采样循环直到 Stop 被调用。只会退出一次。
func (s *Sampler) Run() {
	defer close(s.stopped)
	last := s.clock()
	for {
		select {
		case <-s.done:
			return
		case _, ok := <-s.tk.Chan():
			if !ok {
				return
			}
			now := s.clock()
			if d := now.Sub(last); d <= 0 { // 时钟回拨或零间隔：跳过本次采样
				s.addStat(func(st *Stats) { st.BadIntervals++ })
				last = now
				continue
			}
			last = now
			s.addStat(func(st *Stats) { st.Ticks++ })
			s.sample()
		}
	}
}

func (s *Sampler) sample() {
	st, err := s.src.Stack()
	switch {
	case errors.Is(err, ErrBusy):
		s.addStat(func(t *Stats) { t.Dropped++ })
	case err != nil:
		s.addStat(func(t *Stats) { t.Invalid++ })
	default:
		s.mu.Lock()
		s.tree.Insert(st)
		s.mu.Unlock()
	}
}

func (s *Sampler) addStat(f func(*Stats)) {
	s.statMu.Lock()
	f(&s.stats)
	s.statMu.Unlock()
}

// Stop 幂等停止采样并等待 Run 退出；多次调用效果等同一次。
func (s *Sampler) Stop() {
	s.once.Do(func() {
		s.tk.Stop()
		close(s.done)
	})
	<-s.stopped
}

// Snapshot 返回调用树的一致性深拷贝，供归因查询并发使用。
func (s *Sampler) Snapshot() *tree.Tree {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tree.Snapshot()
}

// Stats 返回当前计数器快照。
func (s *Sampler) Stats() Stats {
	s.statMu.Lock()
	defer s.statMu.Unlock()
	return s.stats
}

// Interval 返回期望采样间隔。
func (s *Sampler) Interval() time.Duration { return s.interval }

type realTicker struct{ tk *time.Ticker }

func (r realTicker) Chan() <-chan time.Time { return r.tk.C }
func (r realTicker) Stop()                  { r.tk.Stop() }

// RealTicker 用真实时钟按 interval 产生节拍。
func RealTicker(interval time.Duration) Ticker {
	return realTicker{tk: time.NewTicker(interval)}
}
