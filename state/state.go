// Package state 实现按 Key 组织的状态表：每条目记录值与最后活动时间 last，
// 按墙钟 TTL 过期，支持惰性清除（Get）与主动清除（Cleanup）。依赖 ttl 包。
package state

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/ttl"
)

// ErrEmptyKey 空 Key 被拒绝（哨兵错误，可 errors.Is 判定）。
var ErrEmptyKey = errors.New("state: empty key")

type entry struct {
	val  string
	last int64
}

// item 是过期小顶堆元素；last 与 ents 中不一致即为陈旧元素，弹出即弃。
type item struct {
	last int64
	key  string
}

type pq []item

func (q pq) Len() int           { return len(q) }
func (q pq) Less(i, j int) bool { return q[i].last < q[j].last }
func (q pq) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *pq) Push(x any)        { *q = append(*q, x.(item)) }
func (q *pq) Pop() any {
	old := *q
	x := old[len(old)-1]
	*q = old[:len(old)-1]
	return x
}

// Table 是并发安全的状态表。
type Table struct {
	mu      sync.Mutex
	ttl     int64
	ents    map[string]entry
	q       pq
	scanned int // 最近一次 Cleanup 检查过的条目数（非导出，不出现在公开接口）
}

// New 建表；ttl 的合法性由调用方（api 包）校验。
func New(ttl int64) *Table {
	return &Table{ttl: ttl, ents: make(map[string]entry)}
}

// Put 写入 val；last 单调推进：last = max(旧 last, eventTime)，乱序/迟到事件不回退。
func (t *Table) Put(key, val string, eventTime int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.ents[key]
	if !ok || eventTime > e.last {
		e.last = eventTime
		heap.Push(&t.q, item{last: e.last, key: key})
	}
	e.val = val
	t.ents[key] = e
	return nil
}

// Get 返回当前值；Key 不存在或已过期均为无匹配，过期条目被惰性清除。
func (t *Table) Get(key string, now int64) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.ents[key]
	if !ok {
		return "", false, nil
	}
	if ttl.Expired(now, e.last, t.ttl) {
		delete(t.ents, key)
		return "", false, nil
	}
	return e.val, true, nil
}

// Cleanup 主动清除 now 时刻全部过期条目，返回清除个数。
// 过期条目靠按 last 排序的小顶堆增量定位：扫描数 = 清除数 + O(1)，不全表扫描。
func (t *Table) Cleanup(now int64) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	removed := 0
	t.scanned = 0
	for len(t.q) > 0 {
		top := t.q[0]
		t.scanned++
		cur, ok := t.ents[top.key]
		if !ok || cur.last != top.last { // 陈旧堆元素，弃
			heap.Pop(&t.q)
			continue
		}
		if !ttl.Expired(now, top.last, t.ttl) {
			break
		}
		heap.Pop(&t.q)
		delete(t.ents, top.key)
		removed++
	}
	return removed
}
