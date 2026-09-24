// Package stage 提供管线阶段的通用骨架：有界队列、背压计数、
// 优雅停止（排空在途）与崩溃（立即中止，不排空）。
package stage

import "sync"

// Env 是处理函数可用的运行环境。Kill 通知进程式崩溃。
type Env struct {
	Kill <-chan struct{}
}

// Process 处理一条输入，返回零或多条输出；ok=false 表示丢弃该条。
type Process[In, Out any] func(env Env, in In) (out []Out, ok bool, err error)

// Stage 是一级有界并发阶段。
type Stage[In, Out any] struct {
	in      chan In
	out     chan Out
	kill    chan struct{}
	proc    Process[In, Out]
	wg      sync.WaitGroup
	stop    sync.Once
	killOne sync.Once

	mu       sync.Mutex
	blocks   int // Submit 因队列满而阻塞的次数
	maxQueue int // 历史最大队列驻留数
}

// New 创建容量为 cap 的阶段（cap>=1）并启动 worker。
func New[In, Out any](cap_ int, proc Process[In, Out]) *Stage[In, Out] {
	if cap_ < 1 {
		cap_ = 1
	}
	s := &Stage[In, Out]{
		in:   make(chan In, cap_),
		out:  make(chan Out, cap_),
		kill: make(chan struct{}),
		proc: proc,
	}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *Stage[In, Out]) run() {
	defer s.wg.Done()
	defer close(s.out)
	for {
		select {
		case <-s.kill:
			return
		case v, ok := <-s.in:
			if !ok {
				return
			}
			outs, good, err := s.proc(Env{Kill: s.kill}, v)
			if err != nil {
				s.Kill()
				return
			}
			if !good {
				continue
			}
			for _, o := range outs {
				select {
				case <-s.kill:
					return
				case s.out <- o:
				}
			}
		}
	}
}

// In 返回输入通道。
func (s *Stage[In, Out]) In() chan<- In { return s.in }

// Out 返回输出通道。
func (s *Stage[In, Out]) Out() <-chan Out { return s.out }

// Submit 发送一条输入；队列满时阻塞并计入背压，满容量计入历史峰值。
func (s *Stage[In, Out]) Submit(v In) {
	if len(s.in) == cap(s.in) {
		s.mu.Lock()
		s.blocks++
		s.mu.Unlock()
	}
	s.in <- v
	s.mu.Lock()
	if n := len(s.in); n > s.maxQueue {
		s.maxQueue = n
	}
	s.mu.Unlock()
}

// Close 关闭输入，等在途全部排空后 worker 自动停止。
func (s *Stage[In, Out]) Close() { s.stop.Do(func() { close(s.in) }) }

// Kill 模拟崩溃：立即停止，不排空，可重复调用。
func (s *Stage[In, Out]) Kill() { s.killOne.Do(func() { close(s.kill) }) }

// Wait 等待 worker 退出。
func (s *Stage[In, Out]) Wait() { s.wg.Wait() }

// Stats 返回历史最大驻留数与阻塞次数。
func (s *Stage[In, Out]) Stats() (maxQueued, blocks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxQueue, s.blocks
}
