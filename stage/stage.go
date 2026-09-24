package stage

import (
	"context"
	"sync"
)

// Tracker 跨所有阶段统计“当前在途记录数”及其历史最大值。
// 每条在途记录恰好占用某个有界队列的一个槽位，
// 因此当前值天然不超过各队列容量之和。
type Tracker struct {
	mu  sync.Mutex
	cur int
	max int
}

func NewTracker() *Tracker { return &Tracker{} }

func (t *Tracker) enter() {
	t.mu.Lock()
	t.cur++
	if t.cur > t.max {
		t.max = t.cur
	}
	t.mu.Unlock()
}

func (t *Tracker) leave() {
	t.mu.Lock()
	t.cur--
	t.mu.Unlock()
}

// Max 返回历史最大在途记录数。
func (t *Tracker) Max() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.max
}

// Stage 是一个带容量上限的队列：满时上游发送阻塞（背压），
// 关闭后下游可排空剩余元素（优雅停止）。
type Stage[T any] struct {
	ch chan T
	t  *Tracker
}

func New[T any](cap int, t *Tracker) *Stage[T] {
	return &Stage[T]{ch: make(chan T, cap), t: t}
}

// Send 向队列放入一个元素；队列满时阻塞，直到放入成功或 ctx 取消。
func (s *Stage[T]) Send(ctx context.Context, v T) bool {
	select {
	case s.ch <- v:
		s.t.enter()
		return true
	case <-ctx.Done():
		return false
	}
}

// Recv 从队列取出一个元素；ok 为 false 表示队列已关闭且排空。
func (s *Stage[T]) Recv() (v T, ok bool) {
	v, ok = <-s.ch
	if ok {
		s.t.leave()
	}
	return v, ok
}

// Close 关闭队列，已在队列中的元素仍可被 Recv 排空。
func (s *Stage[T]) Close() { close(s.ch) }
