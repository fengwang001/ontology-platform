package stage

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Group 是一个键的中间聚合值。
type Group struct {
	Key string
	Sum float64
}

// Snapshot 是屏障时刻聚合器的全量状态：已消费到 Bar，各组有序求和结果。
type Snapshot struct {
	Bar    int64
	Groups []Group
	Bads   int64
}

// AggConfig 配置聚合器：MaxGroups 为内存分组硬上限，0 表示不限。
// OnBarrier 在处理某屏障、产出快照前调用；返回 true 表示调用方请求在此刻崩溃。
type AggConfig struct {
	MaxGroups int
	OnBarrier func(bar int64) bool
}

// Aggregator 从 in 消费记录并按 Key 求和，遇屏障把全量快照送入 out。
// out 满时聚合器阻塞，背压沿队列一路传导回 source。
type SnapCh struct {
	ch      chan Snapshot
	in      int64
	out     int64
	blocks  int64
	maxInfl int64
}

// NewSnapCh 创建容量为 capacity 的快照通道，capacity 至少为 1。
func NewSnapCh(capacity int) *SnapCh {
	if capacity < 1 {
		capacity = 1
	}
	return &SnapCh{ch: make(chan Snapshot, capacity)}
}

// Send 发送快照，满则阻塞并计数。
func (c *SnapCh) Send(s Snapshot, alive func() bool) bool {
	if int(atomic.LoadInt64(&c.in)-atomic.LoadInt64(&c.out)) >= cap(c.ch) {
		atomic.AddInt64(&c.blocks, 1)
	}
	if !alive() {
		return false
	}
	c.ch <- s
	n := atomic.AddInt64(&c.in, 1) - atomic.LoadInt64(&c.out)
	for {
		m := atomic.LoadInt64(&c.maxInfl)
		if n <= m || atomic.CompareAndSwapInt64(&c.maxInfl, m, n) {
			break
		}
	}
	return true
}

// C 返回只读 channel。
func (c *SnapCh) C() <-chan Snapshot { return c.ch }

// DoneOut 由消费方在取出一条后调用。
func (c *SnapCh) DoneOut() { atomic.AddInt64(&c.out, 1) }

// Close 关闭通道。
func (c *SnapCh) Close() { close(c.ch) }

// Blocks 返回历史阻塞次数。
func (c *SnapCh) Blocks() int { return int(atomic.LoadInt64(&c.blocks)) }

// MaxInflight 返回历史最大在途快照数。
func (c *SnapCh) MaxInflight() int { return int(atomic.LoadInt64(&c.maxInfl)) }

type Aggregator struct {
	in     *Queue
	out    *SnapCh
	cfg    AggConfig
	groups map[string]float64
	bads   int64
	wg     sync.WaitGroup
	mu     sync.Mutex
	err    error
}

// NewAggregator 创建并启动聚合器。
func NewAggregator(in *Queue, out *SnapCh, cfg AggConfig) *Aggregator {
	a := &Aggregator{in: in, out: out, cfg: cfg, groups: map[string]float64{}}
	a.wg.Add(1)
	go a.run()
	return a
}

func (a *Aggregator) run() {
	defer a.wg.Done()
	for it := range a.in.C() {
		a.in.DoneOut()
		if it.Bar > 0 {
			if a.cfg.OnBarrier != nil && a.cfg.OnBarrier(it.Bar) {
				continue
			}
			if !a.emit(it.Bar) {
				return
			}
			continue
		}
		sum, ok := a.groups[it.Key]
		if !ok && a.cfg.MaxGroups > 0 && len(a.groups) >= a.cfg.MaxGroups {
			a.setErr(ErrTooManyGroups)
			continue
		}
		a.groups[it.Key] = sum + it.Val
	}
}

func (a *Aggregator) emit(bar int64) bool {
	snap := Snapshot{Bar: bar, Bads: a.bads}
	keys := make([]string, 0, len(a.groups))
	for k := range a.groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	snap.Groups = make([]Group, 0, len(keys))
	for _, k := range keys {
		snap.Groups = append(snap.Groups, Group{Key: k, Sum: a.groups[k]})
	}
	return a.out.Send(snap, func() bool { return true })
}

// AddBad 由上游解析阶段在发现坏记录时调用（并发安全）。
func (a *Aggregator) AddBad(n int64) { atomic.AddInt64(&a.bads, n) }

// Restore 用检查点状态重建聚合器内存并设置屏障基线。
func (a *Aggregator) Restore(groups []Group, bads int64) {
	for _, g := range groups {
		a.groups[g.Key] = g.Sum
	}
	a.bads = bads
}

func (a *Aggregator) setErr(err error) {
	a.mu.Lock()
	if a.err == nil {
		a.err = err
	}
	a.mu.Unlock()
}

// Wait 等待 worker 退出并返回处理错误。
func (a *Aggregator) Wait() error {
	a.wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}
