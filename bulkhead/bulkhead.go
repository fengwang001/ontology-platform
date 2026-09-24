// Package bulkhead 为单个下游提供独立的并发额度与等待队列上限。
package bulkhead

import (
	"context"
	"errors"
)

var (
	// ErrBulkheadFull 表示在途与等待名额均满，调用被立即拒绝。
	ErrBulkheadFull = errors.New("bulkhead: capacity full")
	// ErrBulkheadCanceled 表示等待期间被上游取消。
	ErrBulkheadCanceled = errors.New("bulkhead: canceled while waiting")
	// ErrInvalidConfig 表示 N/Q 配置非法。
	ErrInvalidConfig = errors.New("bulkhead: invalid config")
)

// Bulkhead 是一个下游的舱壁。
type Bulkhead struct {
	slots chan struct{} // 在途名额，容量 N
	queue chan struct{} // 等待名额，容量 Q
	n     int

	mu       chan struct{} // 用 channel 互斥保护 peak/inflight
	inflight int
	peak     int
}

// New 创建舱壁：n 为并发额度（必须 >0），q 为等待队列上限（必须 >=0）。
func New(n, q int) (*Bulkhead, error) {
	if n <= 0 || q < 0 {
		return nil, ErrInvalidConfig
	}
	b := &Bulkhead{
		slots: make(chan struct{}, n),
		queue: make(chan struct{}, q),
		n:     n,
		mu:    make(chan struct{}, 1),
	}
	b.mu <- struct{}{}
	return b, nil
}

// Acquire 申请一个在途名额；满了则进入最多 Q 个位置的等待队列，
// 队列也满时立即返回 ErrBulkheadFull；等待期间 ctx 取消则退出队列。
// 返回的 release 必须且只需调用一次。
func (b *Bulkhead) Acquire(ctx context.Context) (release func(), err error) {
	// 先非阻塞抢在途名额，有名额则立即通过（此时与 Q 无关）。
	select {
	case b.slots <- struct{}{}:
	default:
		// 抢不到则占一个等待名额：非阻塞，第 N+Q+1 个请求立即被拒。
		select {
		case b.queue <- struct{}{}:
		default:
			return nil, ErrBulkheadFull
		}
		select {
		case b.slots <- struct{}{}:
		case <-ctx.Done():
			<-b.queue
			return nil, ErrBulkheadCanceled
		}
		<-b.queue // 拿到在途名额，让出等待名额
	}
	b.enter()
	var done bool
	return func() {
		if done {
			return
		}
		done = true
		<-b.slots
		b.leave()
	}, nil
}

func (b *Bulkhead) enter() {
	<-b.mu
	b.inflight++
	if b.inflight > b.peak {
		b.peak = b.inflight
	}
	b.mu <- struct{}{}
}

func (b *Bulkhead) leave() {
	<-b.mu
	b.inflight--
	b.mu <- struct{}{}
}

// Inflight 返回当前在途真实调用数。
func (b *Bulkhead) Inflight() int {
	<-b.mu
	n := b.inflight
	b.mu <- struct{}{}
	return n
}

// Available 返回当前可用在途名额。
func (b *Bulkhead) Available() int { return b.n - b.Inflight() }

// Peak 返回历史在途峰值（测试用）。
func (b *Bulkhead) Peak() int {
	<-b.mu
	p := b.peak
	b.mu <- struct{}{}
	return p
}
