// Package refresher 提供按键节流的物化视图刷新器。
//
// 同一键的连续变更在防抖窗口内被合并为一个批次：条数加一、值更新、
// 刷新时刻顺延。视图只在批次刷新时更新，待刷新批次中的最新值对外
// 不可见。逻辑时钟严格单调，所有时间戳不得小于上一次成功操作的时间。
package refresher

import (
	"errors"
	"sort"
	"sync"
)

// 四类非法输入对应的可判定错误，互不相同，可用 errors.Is 判定。
var (
	// ErrNonPositiveParam 表示构造参数（防抖间隔、待刷新键数上限）非正。
	ErrNonPositiveParam = errors.New("refresher: 参数必须为正数")
	// ErrEmptyKey 表示变更的键为空。
	ErrEmptyKey = errors.New("refresher: 键不能为空")
	// ErrClockRegression 表示时间戳小于上一次成功操作的时间。
	ErrClockRegression = errors.New("refresher: 时间戳回退")
	// ErrTooManyPending 表示新建批次会导致待刷新键数超过上限。
	ErrTooManyPending = errors.New("refresher: 待刷新键数超限")
	// ErrClosed 表示刷新器已停机收尾，不再接受新的变更或推进。
	ErrClosed = errors.New("refresher: 已停机")
)

// Record 是一批变更被刷新时产出的刷新记录。
type Record struct {
	Key       string // 被刷新的键
	Value     string // 应用到视图的最终值
	Merged    int    // 该批次合并的变更条数
	RefreshAt int64  // 批次的刷新时刻
	AppliedAt int64  // 实际应用（推进或停机）时刻
}

// Snapshot 是刷新器状态的一致性快照，供并发只读使用。
type Snapshot struct {
	View      map[string]string // 当前物化视图（仅含已刷新的值）
	Pending   int               // 待刷新批次数
	Refreshed int64             // 累计刷新批数
	Clock     int64             // 逻辑时钟（上一次成功操作的时间）
}

// batch 是一个键的待刷新批次。
type batch struct {
	value     string
	count     int
	refreshAt int64
}

// Refresher 是按键节流的物化视图刷新器，可并发使用。
type Refresher struct {
	mu         sync.RWMutex
	debounce   int64
	maxPending int

	clock     int64
	hasClock  bool
	batches   map[string]*batch
	view      map[string]string
	refreshed int64
	closed    bool
}

// New 创建刷新器。debounce 为防抖间隔（变更到达后顺延的时长），
// maxPending 为待刷新键数上限；两者都必须为正数。
func New(debounce int64, maxPending int) (*Refresher, error) {
	if debounce <= 0 || maxPending <= 0 {
		return nil, ErrNonPositiveParam
	}
	return &Refresher{
		debounce:   debounce,
		maxPending: maxPending,
		batches:    make(map[string]*batch),
		view:       make(map[string]string),
	}, nil
}

// Upsert 记录键 key 在时刻 at 的变更。
//
// 若该键已有尚未刷新的批次，则并入：条数加一、值更新为最新值、
// 刷新时刻顺延为 at+debounce；否则新建批次。视图不在此更新。
// at 不得小于逻辑时钟；同一键时间戳并列时按到达顺序后者胜。
func (r *Refresher) Upsert(key, value string, at int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrClosed
	}
	if key == "" {
		return ErrEmptyKey
	}
	if r.hasClock && at < r.clock {
		return ErrClockRegression
	}

	if b, ok := r.batches[key]; ok {
		b.count++
		b.value = value
		b.refreshAt = at + r.debounce
	} else {
		if len(r.batches) >= r.maxPending {
			return ErrTooManyPending
		}
		r.batches[key] = &batch{value: value, count: 1, refreshAt: at + r.debounce}
	}

	r.clock = at
	r.hasClock = true
	return nil
}

// Advance 推进到时刻 now，刷新所有刷新时刻不超过 now 的批次
// （含等号边界），每批产出一条刷新记录并应用到视图。
// 返回本次刷新的记录，按键名字典序排列以保证可复现。
func (r *Refresher) Advance(now int64) ([]Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, ErrClosed
	}
	if r.hasClock && now < r.clock {
		return nil, ErrClockRegression
	}

	var keys []string
	for k, b := range r.batches {
		if b.refreshAt <= now {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	records := make([]Record, 0, len(keys))
	for _, k := range keys {
		b := r.batches[k]
		delete(r.batches, k)
		r.view[k] = b.value
		r.refreshed++
		records = append(records, Record{
			Key:       k,
			Value:     b.value,
			Merged:    b.count,
			RefreshAt: b.refreshAt,
			AppliedAt: now,
		})
	}

	r.clock = now
	r.hasClock = true
	return records, nil
}

// Shutdown 停机收尾：把全部待刷新批次立即刷新（应用时刻为当前
// 逻辑时钟），此后刷新器关闭，不再接受变更或推进。重复调用是
// 安全的，后续调用返回空记录。
func (r *Refresher) Shutdown() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true

	keys := make([]string, 0, len(r.batches))
	for k := range r.batches {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	records := make([]Record, 0, len(keys))
	for _, k := range keys {
		b := r.batches[k]
		delete(r.batches, k)
		r.view[k] = b.value
		r.refreshed++
		records = append(records, Record{
			Key:       k,
			Value:     b.value,
			Merged:    b.count,
			RefreshAt: b.refreshAt,
			AppliedAt: r.clock,
		})
	}
	return records
}

// View 返回键 key 在物化视图中的当前值。待刷新批次中的最新值
// 在刷新前不可见。
func (r *Refresher) View(key string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.view[key]
	return v, ok
}

// Snapshot 返回刷新器状态的一致性快照，可并发调用。
func (r *Refresher) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	view := make(map[string]string, len(r.view))
	for k, v := range r.view {
		view[k] = v
	}
	return Snapshot{
		View:      view,
		Pending:   len(r.batches),
		Refreshed: r.refreshed,
		Clock:     r.clock,
	}
}
