// Package bar 在 phase 状态机之上提供并发安全的动态登记/注销时序：
// Register 加入当前相位、ArriveAndDeregister 先到达后注销、AwaitAdvance 等待推进。
package bar

import (
	"sync"

	"ontology/phase"
)

// Bar 是并发安全的动态登记屏障。
type Bar struct {
	mu   sync.Mutex
	cond *sync.Cond
	st   *phase.State
}

// New 创建含 n 个 party 的 Bar。
func New(n int) (*Bar, error) {
	st, err := phase.New(n)
	if err != nil {
		return nil, err
	}
	b := &Bar{st: st}
	b.cond = sync.NewCond(&b.mu)
	return b, nil
}

// Register 新增 party 并加入当前相位，返回（新 id，当前相位）。
func (b *Bar) Register() (int, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id, ph, err := b.st.Register()
	b.cond.Broadcast()
	return id, ph, err
}

// Arrive 标记 id 在当前相位已到达，返回到达时的相位（推进前）。
func (b *Bar) Arrive(id int) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ph, err := b.st.Arrive(id)
	b.cond.Broadcast()
	return ph, err
}

// ArriveAndDeregister 先按 Arrive 语义到达（可能触发推进），再注销 id。
func (b *Bar) ArriveAndDeregister(id int) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ph, err := b.st.Arrive(id)
	if err != nil {
		return ph, err
	}
	err = b.st.Deregister(id)
	b.cond.Broadcast()
	return ph, err
}

// AwaitAdvance 阻塞直到相位 > p，返回推进后的当前相位；终止态返回 -1。
func (b *Bar) AwaitAdvance(p int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	for !b.st.Terminated() && b.st.Phase() <= p {
		b.cond.Wait()
	}
	return b.st.Phase()
}

// Phase 返回当前相位；终止态返回 -1。
func (b *Bar) Phase() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st.Phase()
}

// Snapshot 返回（相位，party 集合，未到达集合）。
func (b *Bar) Snapshot() (int, []int, []int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st.Snapshot()
}
