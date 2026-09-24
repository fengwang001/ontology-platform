// Package batcher 把并发到来的写请求按「条数 / 字节 / 等待时长」三条件攒成批。
package batcher

import (
	"errors"
	"time"

	"ontology/req"
)

// ErrClosed 在 Close 之后（或与之竞争失败时）由 Submit 返回。
var ErrClosed = errors.New("batcher: closed")

// Config 控制攒批触发条件。MaxCount、MaxBytes 必须为正；MaxWait 为 0 表示立即成批。
type Config struct {
	MaxCount int
	MaxBytes int
	MaxWait  time.Duration
}

// Batch 是一次封口的批次，Items 顺序即到达顺序（亦即批内序号顺序）。
type Batch struct {
	Items []req.Pending
	Bytes int
}

// Clock 抽象时间，便于测试注入。
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer 是可停止的定时器。
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type wallTimer struct{ t *time.Timer }

func (w wallTimer) C() <-chan time.Time { return w.t.C }
func (w wallTimer) Stop() bool          { return w.t.Stop() }

// wallClock 使用真实时间。
type wallClock struct{}

func (wallClock) Now() time.Time                        { return time.Now() }
func (wallClock) NewTimer(d time.Duration) Timer        { return wallTimer{time.NewTimer(d)} }

// Batcher 单协程维护当前批，所有状态只在 run 中变更。
type Batcher struct {
	cfg    Config
	clock  Clock
	input  chan req.Pending
	out    chan Batch
	closed chan struct{}
	done   chan struct{}
}

// New 创建 Batcher。clock 为 nil 时使用墙钟。
func New(cfg Config, clock Clock) *Batcher {
	if clock == nil {
		clock = wallClock{}
	}
	b := &Batcher{
		cfg:    cfg,
		clock:  clock,
		input:  make(chan req.Pending),
		out:    make(chan Batch, 1),
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go b.run()
	return b
}

// Submit 追加一条请求；关闭竞争失败时返回 ErrClosed。
func (b *Batcher) Submit(p req.Pending) error {
	select {
	case <-b.closed:
		return ErrClosed
	default:
	}
	select {
	case b.input <- p:
		return nil
	case <-b.closed:
		return ErrClosed
	}
}

// Batches 返回已封口批次通道，Close 排空后关闭。
func (b *Batcher) Batches() <-chan Batch { return b.out }

// Close 通知攒批协程排空当前批并退出。
func (b *Batcher) Close() {
	close(b.closed)
	<-b.done
}

func (b *Batcher) size(items []req.Pending) int {
	n := 0
	for _, it := range items {
		n += len(it.Req.Payload)
	}
	return n
}

func (b *Batcher) flush(items []req.Pending, bytes int) {
	if len(items) == 0 {
		return
	}
	b.out <- Batch{Items: items, Bytes: bytes}
}

func (b *Batcher) run() {
	defer close(b.done)
	var cur []req.Pending
	var timer <-chan time.Time
	for {
		select {
		case p := <-b.input:
			size := len(p.Req.Payload)
			if len(cur) > 0 && b.size(cur)+size > b.cfg.MaxBytes {
				b.flush(cur, b.size(cur))
				cur, timer = nil, nil
			}
			cur = append(cur, p)
			n := len(cur)
			bytes := b.size(cur)
			switch {
			case n >= b.cfg.MaxCount:
				b.flush(cur, bytes)
				cur, timer = nil, nil
			case n == 1 && bytes > b.cfg.MaxBytes:
				b.flush(cur, bytes)
				cur, timer = nil, nil
			case n == 1 && b.cfg.MaxWait > 0 && timer == nil:
				timer = b.clock.NewTimer(b.cfg.MaxWait).C()
			case n == 1 && b.cfg.MaxWait <= 0:
				b.flush(cur, bytes)
				cur, timer = nil, nil
			}
		case <-timer:
			b.flush(cur, b.size(cur))
			cur, timer = nil, nil
		case <-b.closed:
			for {
				select {
				case p := <-b.input:
					cur = append(cur, p)
				default:
					b.flush(cur, b.size(cur))
					close(b.out)
					return
				}
			}
		}
	}
}
