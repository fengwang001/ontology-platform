// Package sched 提供带并发度上限与 ID 升序就绪队列的调度器，
// 并记录历史并发峰值与就绪判定次数。
package sched

import (
	"container/heap"
	"sync"
)

// Scheduler 控制同时运行任务的数量。所有方法由引擎主循环串行调用，
// 计数器用互斥保护以便任务 goroutine 并发只读观察。
type Scheduler struct {
	max     int
	mu      sync.Mutex
	ready   idHeap
	running int
	peak    int
	decide  int

	// start 缓冲为 max：可启动的任务 id 送入后由 Launch 取出。
	start chan string
	// done 缓冲为任务总数：任务完成后回写 id。
	done chan string
}

// New 创建调度器。max<1 时视为 1；slots 为总任务数（用于通道缓冲）。
func New(max, slots int) *Scheduler {
	if max < 1 {
		max = 1
	}
	if slots < 1 {
		slots = 1
	}
	s := &Scheduler{
		max:   max,
		start: make(chan string, max),
		done:  make(chan string, slots),
	}
	heap.Init(&s.ready)
	return s
}

// Enqueue 把一个就绪任务放入 ID 升序队列，并尽量派发（记一次就绪判定）。
func (s *Scheduler) Enqueue(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	heap.Push(&s.ready, id)
	s.dispatch()
}

// dispatch 调用方持锁：有空槽则派发堆顶，直到满或队列空。
// 每次「从就绪堆做出启动/保留判定」记一次 decide。
func (s *Scheduler) dispatch() {
	for s.ready.Len() > 0 {
		s.decide++
		if s.running >= s.max || len(s.start) == cap(s.start) {
			return
		}
		id := heap.Pop(&s.ready).(string)
		s.running++
		if s.running > s.peak {
			s.peak = s.running
		}
		select {
		case s.start <- id:
		default:
			// 启动通道已满（主循环尚未取走）：退回堆顶，待 Release 后重试。
			heap.Push(&s.ready, id)
			s.running--
			return
		}
	}
}

// Launch 返回派发通道：主循环从此取 id 启动任务 goroutine。
func (s *Scheduler) Launch() <-chan string { return s.start }

// CloseLaunch 关闭派发通道，供引擎收敛后结束启动循环。
func (s *Scheduler) CloseLaunch() { close(s.start) }

// Finish 由任务 goroutine 调用，回写完成任务 id（非阻塞安全：缓冲足够）。
func (s *Scheduler) Finish(id string) { s.done <- id }

// Done 通道：主循环 select 等待任务完成。
func (s *Scheduler) Done() <-chan string { return s.done }

// Release 登记一个任务完成并释放槽位，随后尽量派发等待者。
func (s *Scheduler) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running--
	s.decide++
	s.dispatch()
}

// InFlight 返回运行中与已派发未启动数（用于引擎判断收敛）。
func (s *Scheduler) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running + len(s.start)
}

// DrainReady 取出并清空仍在就绪堆中等待的任务（不派发）。
func (s *Scheduler) DrainReady() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, s.ready.Len())
	for s.ready.Len() > 0 {
		out = append(out, heap.Pop(&s.ready).(string))
	}
	return out
}

// Drop 丢弃一个已派发但引擎决定不启动的任务，归还其并发槽并尝试派发等待者。
func (s *Scheduler) Drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running--
	s.dispatch()
}

// Peak 返回历史最大同时运行任务数（非导出计数的只读访问器）。
func (s *Scheduler) Peak() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

// Decisions 返回就绪判定总次数。
func (s *Scheduler) Decisions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.decide
}

// idHeap 是按 ID 字典序取最小值的就绪堆。
type idHeap []string

func (h idHeap) Len() int           { return len(h) }
func (h idHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h idHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *idHeap) Push(x any) { *h = append(*h, x.(string)) }
func (h *idHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
