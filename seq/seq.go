// Package seq 只负责全序广播的序号分配：持久的 nextSeq（从 1 起、单调不减）
// 以及崩溃后空洞区间 (deliveredUpTo, nextSeq) 的判定。它不依赖其他任何包。
package seq

import "sync"

// Allocator 持有持久状态 nextSeq。崩溃语义由上层保证：Crash 不重建
// Allocator，因此 nextSeq 在崩溃后保留、永不回退。
type Allocator struct {
	mu   sync.Mutex
	next int // 下一个要分配的序号，从 1 起
}

// New 创建分配器，nextSeq 初始为 1。
func New() *Allocator { return &Allocator{next: 1} }

// Alloc 分配下一个全局严格递增序号：seq = nextSeq++。
func (a *Allocator) Alloc() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.next
	a.next++
	return s
}

// Next 返回当前 nextSeq（下一个将被分配的序号）。
func (a *Allocator) Next() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.next
}

// IsGap 判定 s 是否落在崩溃后的空洞区间：deliveredUpTo < s < nextSeq。
func (a *Allocator) IsGap(s, deliveredUpTo int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return deliveredUpTo < s && s < a.next
}
