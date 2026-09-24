// Package stage 提供管线阶段的通用骨架：有界队列、背压、屏障与优雅停止。
package stage

import (
	"sync"
	"sync/atomic"
)

// Msg 是阶段间传递的信封。Barrier 为屏障（不携带业务记录）。
type Msg[T any] struct {
	Seq     int64
	Barrier bool
	V       T
}

// Work 处理一条输入；emit 发出零或多条输出。屏障是否透传由 Work 决定。
type Work[In, Out any] func(in Msg[In], emit func(Msg[Out])) error

// Stage 是一个单协程驱动的有界阶段。
// 在途计数 = 入口队列长度 + 正在处理的 1 条，故内部缓冲取 cap-1，
// 保证在途数硬上界为调用方声明的队列容量 cap。
type Stage[In, Out any] struct {
	name string
	in   chan Msg[In]
	out  chan Msg[Out]
	work Work[In, Out]

	inFlight    int64
	maxInFlight atomic.Int64

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	err      error
}

// New 创建阶段，cap 为声明队列容量（>=1）。
func New[In, Out any](name string, cap int, work Work[In, Out]) *Stage[In, Out] {
	if cap < 1 {
		cap = 1
	}
	return &Stage[In, Out]{
		name: name,
		in:   make(chan Msg[In], cap-1),
		out:  make(chan Msg[Out], cap-1),
		work: work,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// In 返回上游写入端。
func (s *Stage[In, Out]) In() chan<- Msg[In] { return s.in }

// Out 返回下游读取端。
func (s *Stage[In, Out]) Out() <-chan Msg[Out] { return s.out }

// MaxInFlight 返回历史最大在途记录数（不含屏障）。
func (s *Stage[In, Out]) MaxInFlight() int64 { return s.maxInFlight.Load() }

// Done 在阶段协程退出后关闭。
func (s *Stage[In, Out]) Done() <-chan struct{} { return s.done }

// Err 返回阶段异常（Abort 的错误），正常结束为 nil。
func (s *Stage[In, Out]) Err() error { return s.err }

// Abort 请求异常停止：排空停止、丢弃在途、记录错误。幂等。
func (s *Stage[In, Out]) Abort(err error) {
	s.stopOnce.Do(func() {
		if err != nil {
			s.err = err
		}
		close(s.stop)
	})
}

func (s *Stage[In, Out]) stopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *Stage[In, Out]) bump() {
	if s.inFlight > s.maxInFlight.Load() {
		s.maxInFlight.Store(s.inFlight)
	}
}

// Run 由唯一的阶段协程执行，直到入口关闭且排空，或被 Abort。
func (s *Stage[In, Out]) Run() {
	defer close(s.done)
	defer close(s.out)
	for {
		select {
		case <-s.stop:
			s.drainOnAbort()
			return
		case m, ok := <-s.in:
			if !ok {
				return
			}
			if !m.Barrier {
				s.inFlight++
				s.bump()
			}
			if !s.handle(m) {
				s.drainOnAbort()
				return
			}
			if !m.Barrier {
				s.inFlight--
			}
		}
	}
}

// handle 处理单条消息；返回 false 表示已 Abort。
func (s *Stage[In, Out]) handle(m Msg[In]) bool {
	emit := func(o Msg[Out]) {
		select {
		case s.out <- o:
		case <-s.stop:
		}
	}
	if err := s.work(m, emit); err != nil {
		s.Abort(err)
		return false
	}
	return !s.stopped()
}

// drainOnAbort 丢弃入口残留，避免上游协程永久阻塞。
func (s *Stage[In, Out]) drainOnAbort() {
	for {
		select {
		case <-s.in:
		default:
			return
		}
	}
}
