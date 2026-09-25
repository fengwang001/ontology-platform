// Package pagg 实现单分区的部分聚合：按 key 累加、按 key 升序导出。
// 不依赖其他包。
package pagg

import (
	"sort"
	"sync"
)

// Record 是上游投递的一条记录。Partition 的合法性由上层校验。
type Record struct {
	Partition int
	Key       string
	Value     int64
}

// Entry 是部分/全局结果里的一行：key 与其总和。
type Entry struct {
	Key   string
	Total int64
}

// Partial 是单分区的部分聚合器，可被多个 goroutine 并发使用。
type Partial struct {
	mu  sync.RWMutex
	sum map[string]int64
}

// New 返回一个空的单分区部分聚合器。
func New() *Partial {
	return &Partial{sum: make(map[string]int64)}
}

// Add 把 rec.Value 累加进 rec.Key 的部分和。每条记录恰好调用一次。
func (p *Partial) Add(rec Record) {
	p.mu.Lock()
	p.sum[rec.Key] += rec.Value
	p.mu.Unlock()
}

// Sorted 返回按 key 升序的部分结果快照；并发读到的是一致快照，不会撕裂。
func (p *Partial) Sorted() []Entry {
	p.mu.RLock()
	out := make([]Entry, 0, len(p.sum))
	for k, v := range p.sum {
		out = append(out, Entry{Key: k, Total: v})
	}
	p.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
