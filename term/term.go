package term

import "sync"

// Term 是未完成任务终止检测：接受任务时 Add，终态时 Done，Wait 等到零。
type Term struct {
	mu sync.Mutex
	n  int64
	zc *sync.Cond
}

func New() *Term {
	t := &Term{}
	t.zc = sync.NewCond(&t.mu)
	return t
}

func (t *Term) Add() {
	t.mu.Lock()
	t.n++
	t.mu.Unlock()
}

func (t *Term) Done() {
	t.mu.Lock()
	t.n--
	if t.n == 0 {
		t.zc.Broadcast()
	}
	t.mu.Unlock()
}

func (t *Term) Count() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}

// Wait 阻塞直到所有已接受任务都到达终态（含执行中派生的任务）。
func (t *Term) Wait() {
	t.mu.Lock()
	for t.n > 0 {
		t.zc.Wait()
	}
	t.mu.Unlock()
}
