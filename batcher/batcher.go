// Package batcher 把并发写请求按 条数/字节/等待时长 攒成批（先到者触发）。
package batcher

import (
	"sync"
	"time"

	"ontology/req"
)

// Clock 是可注入的时间源，After 在 d 后投递一个时刻。
type Clock interface {
	After(d time.Duration) <-chan time.Time
}

// RealClock 使用标准库真实时钟。
type RealClock struct{}

// After 实现 Clock。
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Config 控制攒批触发条件。
type Config struct {
	MaxCount int           // 批内条数达到即触发
	MaxBytes int           // 批内载荷字节达到即触发
	MaxWait  time.Duration // 首批请求到达后最长等待
	Clock    Clock         // 缺省用 RealClock
}

// Batch 是一次组装完成、不可再变的批次。
type Batch struct {
	Entries []*req.Req
	Bytes   int // sum(len(Payload))
}

// Batcher 单 goroutine 组装批次，批次从 Out 输出。
type Batcher struct {
	cfg Config

	in       chan *req.Req
	out      chan *Batch
	stopping chan struct{}
	finished chan struct{}
	closeOne sync.Once
}

// New 启动攒批 goroutine。
func New(cfg Config) *Batcher {
	if cfg.MaxCount < 1 {
		cfg.MaxCount = 1
	}
	if cfg.MaxBytes < 1 {
		cfg.MaxBytes = 1
	}
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
	b := &Batcher{
		cfg:      cfg,
		in:       make(chan *req.Req),
		out:      make(chan *Batch),
		stopping: make(chan struct{}),
		finished: make(chan struct{}),
	}
	go b.run()
	return b
}

// Out 返回已完成批次的通道，Close 排空后关闭。
func (b *Batcher) Out() <-chan *Batch { return b.out }

// Submit 加入一条请求；关闭后返回 req.ErrClosed。
func (b *Batcher) Submit(r *req.Req) error {
	select {
	case b.in <- r:
		return nil
	case <-b.stopping:
		return req.ErrClosed
	}
}

// Close 停止接收新请求，排空在途请求成最后一批，返回时保证已交付。
func (b *Batcher) Close() {
	b.closeOne.Do(func() { close(b.stopping) })
	<-b.finished
}

func (b *Batcher) run() {
	defer close(b.finished)
	defer close(b.out)

	var cur *Batch
	var timer <-chan time.Time

	flush := func() {
		if cur != nil {
			b.out <- cur
			cur = nil
			timer = nil
		}
	}
	add := func(r *req.Req) {
		if cur == nil {
			cur = &Batch{}
			timer = b.cfg.Clock.After(b.cfg.MaxWait)
		}
		cur.Entries = append(cur.Entries, r)
		cur.Bytes += len(r.Payload)
		if len(cur.Entries) >= b.cfg.MaxCount || cur.Bytes >= b.cfg.MaxBytes {
			flush()
		}
	}

	for {
		select {
		case r := <-b.in:
			add(r)
		case <-timer:
			flush()
		case <-b.stopping:
			for {
				select {
				case r := <-b.in:
					add(r)
				default:
					flush()
					return
				}
			}
		}
	}
}
