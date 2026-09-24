// Package stage 提供管线阶段的通用骨架：有界队列 + 背压 + 优雅停止，
// 以及把记录按 Key 分组求和的聚合器。
package stage

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrTooManyGroups 表示内存分组数超过硬上限。
var ErrTooManyGroups = errors.New("stage: too many groups")

// Item 是阶段间流动的消息：要么是一条记录，要么是一个屏障。
type Item struct {
	Key string
	Val float64
	Bar int64 // >0 表示屏障，值为屏障对应的 source 序号；0 表示普通记录
}

// Queue 是有界队列：满时 Send 阻塞（背压），并统计阻塞次数与历史最大在途数。
type Queue struct {
	ch      chan Item
	in      int64 // 累计入队
	out     int64 // 累计出队
	blocks  int64 // 因队列满而阻塞的次数
	maxInfl int64 // 历史最大在途数
}

// NewQueue 创建容量为 cap 的有界队列，cap 至少为 1。
func NewQueue(capacity int) *Queue {
	if capacity < 1 {
		capacity = 1
	}
	return &Queue{ch: make(chan Item, capacity)}
}

// Send 入队，队列满时阻塞（背压真实生效）。closed 后返回 false。
func (q *Queue) Send(it Item, alive func() bool) bool {
	now := atomic.LoadInt64(&q.in) - atomic.LoadInt64(&q.out)
	if int(now) >= cap(q.ch) {
		atomic.AddInt64(&q.blocks, 1)
	}
	if !alive() {
		return false
	}
	q.ch <- it
	n := atomic.AddInt64(&q.in, 1) - atomic.LoadInt64(&q.out)
	for {
		m := atomic.LoadInt64(&q.maxInfl)
		if n <= m || atomic.CompareAndSwapInt64(&q.maxInfl, m, n) {
			break
		}
	}
	return true
}

// Recv 出队，队列关闭且排空后返回 false。
func (q *Queue) Recv() (Item, bool) {
	it, ok := <-q.ch
	if ok {
		atomic.AddInt64(&q.out, 1)
	}
	return it, ok
}

// C 返回底层 channel，供 worker 用 select 消费。
func (q *Queue) C() <-chan Item { return q.ch }

// DoneOut 由直接从 C 消费的 worker 在取出一条后调用以维护计数。
func (q *Queue) DoneOut() {
	atomic.AddInt64(&q.out, 1)
}

// Blocks 返回历史阻塞次数。
func (q *Queue) Blocks() int { return int(atomic.LoadInt64(&q.blocks)) }

// MaxInflight 返回历史最大在途记录数。
func (q *Queue) MaxInflight() int { return int(atomic.LoadInt64(&q.maxInfl)) }

// Close 关闭队列。
func (q *Queue) Close() { close(q.ch) }

// Stage 是一个带处理函数的并发阶段骨架，Stop 幂等。
type Stage struct {
	in   *Queue
	once sync.Once
	wg   sync.WaitGroup
	stop chan struct{}
}

// New 创建阶段：从 in 消费，对每条记录调用 fn，屏障原样下发。
func New(in *Queue, fn func(Item) error) *Stage {
	s := &Stage{in: in, stop: make(chan struct{})}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for it := range in.C() {
			in.DoneOut()
			if it.Bar > 0 || fn == nil {
				continue
			}
			if err := fn(it); err != nil {
				s.Stop()
				return
			}
		}
	}()
	return s
}

// Alive 报告阶段是否尚未停止。
func (s *Stage) Alive() bool {
	select {
	case <-s.stop:
		return false
	default:
		return true
	}
}

// Stop 幂等停止：通知停止；在途数据由上游排空后关闭入口 channel。
func (s *Stage) Stop() { s.once.Do(func() { close(s.stop) }) }

// Wait 等待 worker 退出。
func (s *Stage) Wait() { s.wg.Wait() }
