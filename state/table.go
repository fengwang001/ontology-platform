// Package state provides a key-value state table with wall-clock expiry.
package state

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrEmptyKey 表示键为空。
	ErrEmptyKey = errors.New("state: empty key")
	// ErrInvalidTTL 表示过期时长非法（非正）。
	ErrInvalidTTL = errors.New("state: ttl must be positive")
)

type entry struct {
	value        any
	lastActivity time.Time
}

// Table 是并发安全的键值状态表。
type Table struct {
	mu   sync.Mutex
	ttl  time.Duration
	now  func() time.Time
	data map[string]entry
}

// NewTable 创建状态表。ttl 必须为正，否则返回 ErrInvalidTTL。
// clock 为墙钟来源，传 nil 时使用 time.Now。
func NewTable(ttl time.Duration, clock func() time.Time) (*Table, error) {
	if ttl <= 0 {
		return nil, ErrInvalidTTL
	}
	if clock == nil {
		clock = time.Now
	}
	return &Table{ttl: ttl, now: clock, data: make(map[string]entry)}, nil
}

// TTL 返回表的过期时长。
func (t *Table) TTL() time.Duration { return t.ttl }

// expired 判定条目在墙钟时刻 now 是否已过期：
// now - lastActivity >= ttl 即过期（恰好等于也算过期）。
func (t *Table) expired(e entry, now time.Time) bool {
	return now.Sub(e.lastActivity) >= t.ttl
}

// Put 写入键值。事件时间允许乱序到达：最后活动时间取旧值与
// eventTime 的较大者，只进不退。key 为空返回 ErrEmptyKey，
// 失败不改变任何状态。
func (t *Table) Put(key string, value any, eventTime time.Time) error {
	if key == "" {
		return ErrEmptyKey
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.data[key]
	if !ok || eventTime.After(e.lastActivity) {
		e.lastActivity = eventTime
	}
	e.value = value
	t.data[key] = e
	return nil
}

// Get 读取键。若条目在调用时的墙钟时间下已过期，则惰性清除
// 并返回未命中；不存在的键同样返回未命中。返回值与本次调用
// 观察到的墙钟时间及最后活动时间一致，不存在中间态。
func (t *Table) Get(key string) (any, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.data[key]
	if !ok {
		return nil, false
	}
	if t.expired(e, t.now()) {
		delete(t.data, key)
		return nil, false
	}
	return e.value, true
}

// Cleanup 主动清除当前墙钟时间下所有已过期条目，返回清除个数。
func (t *Table) Cleanup() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	removed := 0
	for k, e := range t.data {
		if t.expired(e, now) {
			delete(t.data, k)
			removed++
		}
	}
	return removed
}

// Check 自检：返回当前墙钟时间下已过期（待清理）的条目数，
// 不修改任何状态，可与读写并发调用。
func (t *Table) Check() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	expired := 0
	for _, e := range t.data {
		if t.expired(e, now) {
			expired++
		}
	}
	return expired
}

// Len 返回当前表内条目数（含尚未被清理的过期条目）。
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.data)
}
