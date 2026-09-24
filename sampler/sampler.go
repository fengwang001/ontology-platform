// 采样器：固定间隔 tick，时钟/栈来源/忙窗口均可注入。
package sampler

import (
	"sync"
	"sync/atomic"

	"ontology/stack"
	"ontology/tree"
)

// Sampler 每次 Step 表示一次到点的采样调度。
type Sampler struct {
	tr       *tree.Tree
	maxDepth int
	now      func() int64
	src      func() []string
	busy     func() bool

	mu        sync.Mutex // 保护 last
	last      int64
	ticks     atomic.Int64
	dropped   atomic.Int64
	invalid   atomic.Int64
	anomalous atomic.Int64
	stopOnce  sync.Once
	stopped   atomic.Bool
}

// New 构造采样器。now 返回单调纳秒时间；src 返回一条原始栈；busy 非空且返回 true 时丢样。
func New(tr *tree.Tree, maxDepth int, now func() int64, src func() []string, busy func() bool) *Sampler {
	return &Sampler{tr: tr, maxDepth: maxDepth, now: now, src: src, busy: busy}
}

// Step 执行一次采样调度：时钟回拨计入异常并跳过；忙窗口丢样；空栈计无效；否则规范化后插入树。
func (s *Sampler) Step() {
	if s.stopped.Load() {
		return
	}
	s.ticks.Add(1)
	now := s.now()
	s.mu.Lock()
	if now < s.last {
		s.last = now // 重新对时，间隔计算不会产生负数或巨大值
		s.mu.Unlock()
		s.anomalous.Add(1)
		return
	}
	s.last = now
	s.mu.Unlock()
	if s.busy != nil && s.busy() {
		s.dropped.Add(1)
		return
	}
	frames, trunc, err := stack.Normalize(s.src(), s.maxDepth)
	if err != nil {
		s.invalid.Add(1)
		return
	}
	s.tr.Insert(frames, trunc)
}

// Stop 停止采样，幂等。
func (s *Sampler) Stop() {
	s.stopOnce.Do(func() { s.stopped.Store(true) })
}

// Stopped 报告是否已停止。
func (s *Sampler) Stopped() bool { return s.stopped.Load() }

// Ticks 返回调度总次数，恒等于 samples+dropped+invalid+anomalous。
func (s *Sampler) Ticks() int64 { return s.ticks.Load() }

// Dropped 返回忙窗口丢样数。
func (s *Sampler) Dropped() int64 { return s.dropped.Load() }

// Invalid 返回无效样本（空栈）数。
func (s *Sampler) Invalid() int64 { return s.invalid.Load() }

// Anomalous 返回异常间隔（时钟回拨）数。
func (s *Sampler) Anomalous() int64 { return s.anomalous.Load() }
