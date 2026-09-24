// Package seq 实现序列号分配器：先持久化后发放，并发安全。
package seq

import (
	"sync"

	"ontology/check"
)

// Allocator 顺序发放 0,1,2,...；checkpoint 是发放的提交点。
type Allocator struct {
	mu    sync.Mutex
	next  int64
	store *check.Store
}

// NewAllocator 基于 store 创建分配器，next 初始为 0。
func NewAllocator(store *check.Store) *Allocator {
	return &Allocator{store: store}
}

// Next 先持久化、后发放：
// ① n = next；② 原子持久化 next = n+1；③ 内存 next = n+1；④ 返回 n。
// 第 ② 步失败则整次失败：不返回编号、不推进内存 next（失败不留痕）。
func (a *Allocator) Next() (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := a.next
	if err := a.store.Write(n + 1); err != nil {
		return 0, err
	}
	a.next = n + 1
	return n, nil
}

// SimulateCrash 丢弃内存状态（模拟进程崩溃，仅测试用）。
func (a *Allocator) SimulateCrash() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.next = 0
}

// Recover 读 checkpoint 重建 next；损坏时报错且不改内存状态。
func (a *Allocator) Recover() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	next, err := a.store.Read()
	if err != nil {
		return err
	}
	a.next = next
	return nil
}
