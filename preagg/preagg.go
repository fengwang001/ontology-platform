package preagg

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
)

// 四类非法输入各自对应一个互不相同、可判定的哨兵错误，可用 errors.Is 判定。
var (
	// ErrInvalidThreshold 阈值非正。
	ErrInvalidThreshold = errors.New("threshold must be positive")
	// ErrEmptyKey 事件键为空。
	ErrEmptyKey = errors.New("event key must not be empty")
	// ErrZeroDelta 事件增量为零。
	ErrZeroDelta = errors.New("event delta must not be zero")
	// ErrEmptyBatch 空批次。
	ErrEmptyBatch = errors.New("batch must contain at least one event")
)

// Event 表示一条键的变更事件。
type Event struct {
	Key   string
	Delta int64
}

// pending 是单个键在本地缓冲中尚未推送给全局视图的状态。
type pending struct {
	delta int64 // 待推送增量之和
	count int   // 已攒条数
}

// PreAggregator 是面向倾斜键的两阶段预聚合器。
//
// 阶段一：非热键的事件在本地缓冲内按条数攒批，条数达到阈值后推送。
// 阶段二：热键的每一条事件到达即单独推送，不再攒批。
//
// 所有方法均可被并发调用；View/HotKeys/Verify/Replay 为并发只读快照。
type PreAggregator struct {
	mu        sync.RWMutex
	threshold int
	view      map[string]int64 // 全局累计视图，只包含已推送增量
	buffer    map[string]pending
	hot       map[string]bool
	log       []Event // 推送记录，按推送先后顺序排列；从空视图重放可重建 view

	// fedSum 记录截至当前已喂入事件（含尚未推送的缓冲）按键的分组求和，
	// 供 Verify 交叉校验。
	fedSum map[string]int64
}

// New 创建阈值为 threshold 的预聚合器。阈值非正时返回 ErrInvalidThreshold。
func New(threshold int) (*PreAggregator, error) {
	if threshold <= 0 {
		return nil, ErrInvalidThreshold
	}
	return &PreAggregator{
		threshold: threshold,
		view:      make(map[string]int64),
		buffer:    make(map[string]pending),
		hot:       make(map[string]bool),
		fedSum:    make(map[string]int64),
	}, nil
}

// FeedBatch 原子地喂入一批事件：先整体校验，任一条非法则整批拒绝、不留状态痕迹；
// 全部合法后按到达顺序应用。热键事件立即推送；非热键事件攒批，达到阈值（含阈值）
// 立即推送并成为热键；同一批次内同一键出现两次及以上，该键在批末成为热键
// （批末缓冲的待推送增量先冲刷为一条推送，再标记为热键）。
func (p *PreAggregator) FeedBatch(events []Event) error {
	if len(events) == 0 {
		return ErrEmptyBatch
	}
	for i, e := range events {
		if e.Key == "" {
			return fmt.Errorf("%w: event index %d", ErrEmptyKey, i)
		}
		if e.Delta == 0 {
			return fmt.Errorf("%w: event index %d key %q", ErrZeroDelta, i, e.Key)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	seen := make(map[string]int, len(events))
	for _, e := range events {
		seen[e.Key]++
		p.fedSum[e.Key] += e.Delta
		if p.hot[e.Key] {
			// 热键：每条事件立即单独推送，不经过缓冲。
			p.push(e)
			continue
		}
		b := p.buffer[e.Key]
		b.delta += e.Delta
		b.count++
		if b.count >= p.threshold {
			// 攒满阈值（含阈值本身）：待推送增量作为一条变更推送并清空缓冲，
			// 同时标记为热键。
			p.push(Event{Key: e.Key, Delta: b.delta})
			delete(p.buffer, e.Key)
			p.hot[e.Key] = true
		} else {
			p.buffer[e.Key] = b
		}
	}

	// 批末晋升：本批出现两次及以上的非热键成为热键；先冲刷其缓冲。
	for key, n := range seen {
		if n >= 2 && !p.hot[key] {
			if b, ok := p.buffer[key]; ok {
				p.push(Event{Key: key, Delta: b.delta})
				delete(p.buffer, key)
			}
			p.hot[key] = true
		}
	}
	return nil
}

// push 把一条已推送变更写入全局视图并追加到推送记录。调用方必须持有写锁。
func (p *PreAggregator) push(e Event) {
	p.view[e.Key] += e.Delta
	p.log = append(p.log, e)
}

// FlushKey 显式冲刷单个键：把该键尚有待推送的增量作为一条变更推送，
// 并将其移出热键集合；之后该键重新按普通键攒批。返回该键是否发生了推送。
// 空键返回 ErrEmptyKey，且不改变任何状态。
func (p *PreAggregator) FlushKey(key string) (bool, error) {
	if key == "" {
		return false, ErrEmptyKey
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	b, ok := p.buffer[key]
	if ok {
		p.push(Event{Key: key, Delta: b.delta})
		delete(p.buffer, key)
	}
	delete(p.hot, key)
	return ok, nil
}

// FlushAll 全量冲刷：把所有尚有待推送增量的键按字典序逐个推送，
// 并清空热键集合（所有键移出热键集合）。
func (p *PreAggregator) FlushAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	keys := make([]string, 0, len(p.buffer))
	for key := range p.buffer {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b := p.buffer[key]
		p.push(Event{Key: key, Delta: b.delta})
		delete(p.buffer, key)
	}
	clear(p.hot)
}

// View 返回全局累计视图的快照拷贝；不包含本地尚未推送的缓冲增量。
func (p *PreAggregator) View() map[string]int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneMap(p.view)
}

// Pending 返回各键本地待推送增量的快照拷贝。
func (p *PreAggregator) Pending() map[string]int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]int64, len(p.buffer))
	for k, b := range p.buffer {
		out[k] = b.delta
	}
	return out
}

// HotKeys 返回热键集合的快照拷贝。
func (p *PreAggregator) HotKeys() map[string]bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneBoolMap(p.hot)
}

// PushLog 返回推送记录的快照拷贝，按推送先后顺序排列。
// 从空视图开始按序重放该记录必须精确重建当前 View。
func (p *PreAggregator) PushLog() []Event {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return slices.Clone(p.log)
}

// Replay 从空视图开始按序重放推送记录，返回重建出的视图快照。
func (p *PreAggregator) Replay() map[string]int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]int64)
	for _, e := range p.log {
		out[e.Key] += e.Delta
	}
	return out
}

// Verify 并发只读自检，校验两条不变量：
//  1. 任意键：全局视图值 + 本地待推送增量 == 截至当前已喂事件的分组求和；
//  2. 推送记录从空开始按序重放精确等于当前视图。
func (p *PreAggregator) Verify() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	keys := make(map[string]struct{}, len(p.view)+len(p.buffer)+len(p.fedSum))
	for k := range p.view {
		keys[k] = struct{}{}
	}
	for k := range p.buffer {
		keys[k] = struct{}{}
	}
	for k := range p.fedSum {
		keys[k] = struct{}{}
	}
	for k := range keys {
		if got := p.view[k] + p.buffer[k].delta; got != p.fedSum[k] {
			return fmt.Errorf("invariant violated for key %q: view(%d)+pending(%d)=%d != fedSum(%d)",
				k, p.view[k], p.buffer[k].delta, got, p.fedSum[k])
		}
	}

	replayed := make(map[string]int64)
	for _, e := range p.log {
		replayed[e.Key] += e.Delta
	}
	if len(replayed) != len(p.view) {
		return fmt.Errorf("replay mismatch: replayed has %d keys, view has %d", len(replayed), len(p.view))
	}
	for k, v := range p.view {
		if replayed[k] != v {
			return fmt.Errorf("replay mismatch for key %q: replayed %d != view %d", k, replayed[k], v)
		}
	}
	return nil
}

func cloneMap(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneBoolMap(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
