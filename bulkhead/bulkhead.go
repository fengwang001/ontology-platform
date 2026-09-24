// Package bulkhead 提供按下游隔离的并发额度与等待队列上限。
package bulkhead

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrQueueFull 等待队列（含在途额度）已满，请求被立即拒绝。
	ErrQueueFull = errors.New("bulkhead: concurrency limit and queue full")
)

// waiter 是一个排队等待真实额度的请求。token 容量 1：
// 释放者转交额度时非阻塞发送，取消者据此判断自己是否已被晋升。
type waiter struct {
	token chan struct{}
	ctx   context.Context
}

// Bulkhead 是单个下游独立的舱壁。为不同下游分别建实例即完成隔离。
type Bulkhead struct {
	slots chan struct{} // 容量 N+Q：在途 + 排队的总占位
	run   chan struct{} // 容量 N：真实并发额度

	mu       sync.Mutex
	queue    []*waiter // FIFO 等待队列
	inFlight int
	peak     int // 历史在途峰值（非导出，同包测试可读）
}

// New 创建舱壁：maxConcurrent 为真实并发额度 N，maxQueue 为等待位 Q。
// N 必须 >= 1（N=1 即串行），Q 必须 >= 0。
func New(maxConcurrent, maxQueue int) (*Bulkhead, error) {
	if maxConcurrent <= 0 {
		return nil, errors.New("bulkhead: maxConcurrent must be > 0")
	}
	if maxQueue < 0 {
		return nil, errors.New("bulkhead: maxQueue must be >= 0")
	}
	return &Bulkhead{
		slots: make(chan struct{}, maxConcurrent+maxQueue),
		run:   make(chan struct{}, maxConcurrent),
	}, nil
}

// InFlight 返回当前在途真实调用数。
func (b *Bulkhead) InFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight
}

// Peak 返回历史在途峰值。
func (b *Bulkhead) Peak() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// Acquire 取得真实并发额度。拿不到额度时进入 FIFO 队列等待；
// 总占位（在途+排队）达 N+Q 则立即返回 ErrQueueFull（不等待、不读时钟）；
// ctx 在等待中取消则从队列移除并归还占位。
// 返回的 release 必须在成功、失败、超时、panic 四条路径上各调用一次。
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	// 先占一个总槽位：满了立即拒绝。
	select {
	case b.slots <- struct{}{}:
	default:
		return nil, ErrQueueFull
	}

	// 尝试直接拿真实额度。
	select {
	case b.run <- struct{}{}:
		b.enter()
		return b.release, nil
	default:
	}

	// 真实额度已用尽：排队。
	w := &waiter{token: make(chan struct{}, 1), ctx: ctx}
	b.mu.Lock()
	b.queue = append(b.queue, w)
	b.mu.Unlock()

	select {
	case <-w.token:
		// 释放者已把真实额度（run 槽）转交给我。
		b.enter()
		return b.release, nil
	case <-ctx.Done():
		// 取消：先摘除队列项。若释放者恰已转交（token 有值），
		// 我持有额度，代为继续转交给下一位；否则仅归还总占位。
		b.remove(w)
		select {
		case <-w.token:
			b.passOn()
		default:
		}
		<-b.slots
		return nil, ctx.Err()
	}
}

// enter 在已持有 run 额度后登记在途并更新峰值。
func (b *Bulkhead) enter() {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.peak {
		b.peak = b.inFlight
	}
	b.mu.Unlock()
}

// release 与一次成功 Acquire 配对：退出在途，并把额度转交给队首或归还池。
func (b *Bulkhead) release() {
	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
	b.passOn()
	<-b.slots
}

// passOn 把一个 run 额度交给队首等待者；无等待者则归还 run 池。
func (b *Bulkhead) passOn() {
	for {
		b.mu.Lock()
		if len(b.queue) == 0 {
			b.mu.Unlock()
			<-b.run
			return
		}
		w := b.queue[0]
		b.queue = b.queue[1:]
		b.mu.Unlock()

		// token 容量 1：等待者活跃时必为空，发送即晋升；
		// 若等待者已取消（remove 已摘除它），它不会出现在队列里，
		// 因此队首只可能是活跃等待者。
		select {
		case w.token <- struct{}{}:
			return
		default:
			// 理论不可达：已取消者已被 remove 摘除。
		}
	}
}

// remove 从 FIFO 队列摘除指定等待者。
func (b *Bulkhead) remove(w *waiter) {
	b.mu.Lock()
	for i, q := range b.queue {
		if q == w {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			break
		}
	}
	b.mu.Unlock()
}
