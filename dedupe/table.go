// Package dedupe 提供容量 K 的有界去重表（uid -> 不透明结果）。
//
// 淘汰策略：按首次登记顺序 FIFO 淘汰最旧项；命中不刷新，即查找不会改变
// 淘汰顺序。若对已存在的键重新 Put，仅更新值并把该条目移到最新位置。
package dedupe

import "sync"

// Table 是并发安全的有界去重表。
type Table struct {
	mu  sync.Mutex
	k   int
	pos map[string]int
	ord []string // FIFO 登记顺序，头最旧、尾最新
	val map[string]any
}

// New 创建容量 k（1..10^6）的去重表；越界返回 nil。
func New(k int) *Table {
	if k < 1 || k > 1_000_000 {
		return nil
	}
	return &Table{
		k:   k,
		pos: make(map[string]int),
		ord: make([]string, 0, k),
		val: make(map[string]any),
	}
}

// Lookup 返回 uid 的登记结果；不存在时 ok 为 false。命中不刷新淘汰顺序。
func (t *Table) Lookup(uid []byte) (any, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.val[string(uid)]
	return v, ok
}

// Put 登记键值：新键按 FIFO 加入并在超容量时淘汰最旧项；
// 已存在的键仅更新值并移到最新位置。
func (t *Table) Put(uid []byte, v any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := string(uid)
	if _, ok := t.pos[key]; ok {
		t.val[key] = v
		t.moveToBack(key)
		return
	}
	t.ord = append(t.ord, key)
	t.pos[key] = len(t.ord) - 1
	t.val[key] = v
	if len(t.ord) > t.k {
		oldest := t.ord[0]
		t.ord = t.ord[1:]
		delete(t.pos, oldest)
		delete(t.val, oldest)
		for i := range t.ord {
			t.pos[t.ord[i]] = i
		}
	}
}

// Len 返回当前登记项数（不超过容量 K）。
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.ord)
}

// moveToBack 把已存在的键移到登记顺序的最新端。调用方持锁。
func (t *Table) moveToBack(key string) {
	idx := t.pos[key]
	copy(t.ord[idx:], t.ord[idx+1:])
	t.ord[len(t.ord)-1] = key
	for i := idx; i < len(t.ord); i++ {
		t.pos[t.ord[i]] = i
	}
}
