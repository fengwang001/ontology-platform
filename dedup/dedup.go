// Package dedup 维护 ID → last 的去重表：窗口判定委托 win 包，
// 表满时淘汰 last 最小者，并统计接受/重复计数。
package dedup

import (
	"errors"

	"ontology/win"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmptyID            = errors.New("dedup: empty id")
	ErrNonPositiveWindow  = errors.New("dedup: window must be > 0")
	ErrNonPositiveMaxOpen = errors.New("dedup: maxOpen must be > 0")
)

// Table 是滑动去重窗口的去重表。不是并发安全的，并发封装在 api 包。
type Table struct {
	w          int64
	maxOpen    int
	last       map[string]int64
	accepted   int64
	duplicated int64
	checked    int // 最近一次 Dedup 为定位 ID 检查过的表条目个数（非导出，map 定位恒为 1）
}

// New 构造去重表；w<=0 或 maxOpen<=0 整体失败，不留任何状态。
func New(w int64, maxOpen int) (*Table, error) {
	if w <= 0 {
		return nil, ErrNonPositiveWindow
	}
	if maxOpen <= 0 {
		return nil, ErrNonPositiveMaxOpen
	}
	return &Table{w: w, maxOpen: maxOpen, last: make(map[string]int64)}, nil
}

// Dedup 判定一个事件。返回 accepted；空 ID 返回 ErrEmptyID 且不改任何状态。
func (t *Table) Dedup(id string, ts int64) (bool, error) {
	if id == "" {
		return false, ErrEmptyID
	}
	t.checked = 1 // map 定位：只检查目标条目一个
	last, ok := t.last[id]
	if !ok { // 新 ID：接受；表满先淘汰 last 最小者
		if len(t.last) >= t.maxOpen {
			t.evictOldest()
		}
		t.last[id] = ts
		t.accepted++
		return true, nil
	}
	if win.Duplicate(last, ts, t.w) { // 重复/乱序：不刷新 last、不触发淘汰
		t.duplicated++
		return false, nil
	}
	t.last[id] = ts
	t.accepted++
	return true, nil
}

// evictOldest 淘汰 last 最小的 ID；并列时取 ID 字典序最小者，保证确定性。
func (t *Table) evictOldest() {
	var minID string
	var minLast int64
	first := true
	for id, last := range t.last {
		if first || last < minLast || (last == minLast && id < minID) {
			minID, minLast, first = id, last, false
		}
	}
	delete(t.last, minID)
}

// View 返回当前保留的 ID → last 的副本。
func (t *Table) View() map[string]int64 {
	out := make(map[string]int64, len(t.last))
	for id, last := range t.last {
		out[id] = last
	}
	return out
}

// Accepted 返回累计接受数。
func (t *Table) Accepted() int64 { return t.accepted }

// Duplicated 返回累计重复数。
func (t *Table) Duplicated() int64 { return t.duplicated }
