// Package stat 聚合计数并编排 熔断→舱壁→时限调用 三层保护。
package stat

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/classify"
	"ontology/timeout"
)

// Counters 是一次保护器生命周期内的全部计数。
type Counters struct {
	Total            int64
	Real             int64 // 真实调用数
	BreakerRejected  int64
	BulkheadRejected int64
	Success          int64
	FailRetryable    int64
	FailNonRetryable int64
	FailTimeout      int64
	FailPanic        int64
}

// Failure 返回四类失败之和。
func (c Counters) Failure() int64 {
	return c.FailRetryable + c.FailNonRetryable + c.FailTimeout + c.FailPanic
}

// Downstream 是一个下游的完整保护配置。
type Downstream struct {
	Name        string
	Concurrency int
	Queue       int
	CallTimeout time.Duration
	Breaker     breaker.Config
}

// Protector 管理多个互相独立的下游。
type Protector struct {
	mu       sync.RWMutex
	down     map[string]*downGuard
	counters atomic.Value // Counters
}

type downGuard struct {
	br *breaker.Breaker
	bh *bulkhead.Bulkhead
	d  time.Duration
}

// New 构造保护器。
func New() *Protector {
	p := &Protector{down: make(map[string]*downGuard)}
	p.counters.Store(Counters{})
	return p
}

func (p *Protector) add(fn func(*Counters)) {
	for {
		cur := p.counters.Load().(Counters)
		next := cur
		fn(&next)
		if p.counters.CompareAndSwap(cur, next) {
			return
		}
	}
}

// Register 添加一个下游；参数非法时返回错误。
func (p *Protector) Register(d Downstream) error {
	if d.Concurrency <= 0 || d.Queue <= 0 || d.CallTimeout <= 0 {
		return errors.New("stat: invalid bulkhead or timeout config")
	}
	br, err := breaker.New(d.Breaker)
	if err != nil {
		return err
	}
	bh, err := bulkhead.New(d.Concurrency, d.Queue)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.down[d.Name] = &downGuard{br: br, bh: bh, d: d.CallTimeout}
	p.mu.Unlock()
	return nil
}

// ErrUnknownDownstream 表示下游未注册。
var ErrUnknownDownstream = errors.New("stat: unknown downstream")

// Do 按 熔断→舱壁→真实调用 的顺序执行 fn，并更新计数。
// 每个请求恰落入 成功/失败/熔断拒绝/舱壁拒绝 之一。
func (p *Protector) Do(ctx context.Context, name string,
	fn func(ctx context.Context) error) error {
	p.mu.RLock()
	g := p.down[name]
	p.mu.RUnlock()
	if g == nil {
		return ErrUnknownDownstream
	}

	p.add(func(c *Counters) { c.Total++ })

	// 1) 熔断在前：打开/回拨立即拒绝，不占用舱壁额度。
	if err := g.br.Allow(); err != nil {
		p.add(func(c *Counters) { c.BreakerRejected++ })
		return err
	}

	// 2) 舱壁：取不到额度（队列满）立即拒绝，不计失败、不报熔断。
	if err := g.bh.Acquire(ctx); err != nil {
		if errors.Is(err, bulkhead.ErrBulkheadRejected) {
			p.add(func(c *Counters) { c.BulkheadRejected++ })
			return err
		}
		// 上游在排队期间取消：不计入任何拒绝/失败（请求未真正发生），
		// 但总额已计；为保持等式，归入舱壁拒绝的一种可判定结果。
		p.add(func(c *Counters) { c.BulkheadRejected++ })
		return err
	}

	// 3) 真实调用：四条路径都要归还额度。
	p.add(func(c *Counters) { c.Real++ })
	var callErr error
	func() {
		defer g.bh.Release()
		callErr = timeout.Run(ctx, g.d, fn)
	}()

	if callErr == nil {
		g.br.Success()
		p.add(func(c *Counters) { c.Success++ })
		return nil
	}
	g.br.Failure()
	switch classify.Classify(callErr) {
	case classify.NonRetryable:
		p.add(func(c *Counters) { c.FailNonRetryable++ })
	case classify.Timeout:
		p.add(func(c *Counters) { c.FailTimeout++ })
	case classify.PanicKind:
		p.add(func(c *Counters) { c.FailPanic++ })
	default:
		p.add(func(c *Counters) { c.FailRetryable++ })
	}
	return callErr
}

// Snapshot 返回当前计数的副本。
func (p *Protector) Snapshot() Counters { return p.counters.Load().(Counters) }

// Bulkhead 暴露下游舱壁（供演示读取占用）。
func (p *Protector) Bulkhead(name string) *bulkhead.Bulkhead {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if g, ok := p.down[name]; ok {
		return g.bh
	}
	return nil
}

// Breaker 暴露下游熔断器（供演示读取状态）。
func (p *Protector) Breaker(name string) *breaker.Breaker {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if g, ok := p.down[name]; ok {
		return g.br
	}
	return nil
}
