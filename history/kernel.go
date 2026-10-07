// Package history 实现浏览器会话历史栈与前进后退缓存（BFCache）的协调内核。
//
// 内核由五部分协作构成：历史条目列表与当前位置（Kernel.entries/pos）、
// 遍历调度（traverse.go 中的合并队列）、缓存资格判定（document.go 的 docFlags）、
// 缓存容量与存活期淘汰（cache.go 的最小堆）。所有公开方法持有同一把互斥锁，
// 并发调用等价于某个串行顺序。
package history

import (
	"fmt"
	"sort"
	"sync"
)

// Config 是缓存策略配置。
type Config struct {
	Capacity int   // 缓存文档数上限，0 表示禁用缓存；负数报参数非法
	TTL      int64 // 缓存存活时长（时钟单位），0 表示不限；负数报参数非法
}

// Kernel 是历史与缓存的协调内核，并发安全。
type Kernel struct {
	mu sync.Mutex

	entries []Entry
	pos     int // 无条目时为 -1
	now     int64

	capacity int
	ttl      int64

	entrySeq  uint64
	docSeq    uint64
	insertSeq uint64

	docs    map[string]*document
	ix      entryIndex
	cache   docCache
	pending []pendingTraverse
}

// NewKernel 构造内核；负上限或负存活期返回参数非法。
func NewKernel(cfg Config) (*Kernel, error) {
	if cfg.Capacity < 0 {
		return nil, errf(KindInvalidArgument, "capacity %d is negative", cfg.Capacity)
	}
	if cfg.TTL < 0 {
		return nil, errf(KindInvalidArgument, "ttl %d is negative", cfg.TTL)
	}
	return &Kernel{
		pos:      -1,
		capacity: cfg.Capacity,
		ttl:      cfg.TTL,
		docs:     make(map[string]*document),
		ix:       newEntryIndex(),
	}, nil
}

// Navigate 推入导航：在当前位置之后追加新条目并截断其后全部条目。
// sameDoc 为 true 表示同文档导航（仅片段或仅状态变化），新条目与当前条目
// 共享文档标识，当前文档不进入缓存也不被卸载。
func (k *Kernel) Navigate(url string, state any, sameDoc bool) (Entry, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if url == "" {
		return Entry{}, errf(KindInvalidArgument, "empty url")
	}
	k.truncateLocked()
	k.entrySeq++
	e := Entry{URL: url, State: state, Seq: k.entrySeq}
	if sameDoc && k.pos >= 0 {
		e.DocID = k.entries[k.pos].DocID
	} else {
		if k.pos >= 0 {
			k.leaveCurrentLocked()
		}
		e.DocID = k.allocDocLocked()
	}
	k.entries = append(k.entries, e)
	k.pos++
	k.ix.add(e.DocID, k.pos)
	k.enforceCapacityLocked()
	return e, nil
}

// Replace 替换导航：只修改当前条目的地址与状态对象，序号与位置不变。
func (k *Kernel) Replace(url string, state any) (Entry, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if url == "" {
		return Entry{}, errf(KindInvalidArgument, "empty url")
	}
	if k.pos < 0 {
		return Entry{}, errf(KindInvalidState, "no current entry to replace")
	}
	k.entries[k.pos].URL = url
	k.entries[k.pos].State = state
	return k.entries[k.pos], nil
}

// AdvanceClock 推进时钟并驱逐存活期已到的缓存文档；负增量报时钟回退。
func (k *Kernel) AdvanceClock(delta int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if delta < 0 {
		return errf(KindClockRollback, "negative clock delta %d", delta)
	}
	k.now += delta
	k.evictExpiredLocked()
	return nil
}

// SetNetworkPending 设置"有未完成网络事务"条件。
func (k *Kernel) SetNetworkPending(at int64, docID string, v bool) error {
	return k.mutateDoc(at, docID, func(d *document) { d.flags.networkPending = v })
}

// SetUnloadBlocker 设置"已注册卸载阻止回调"条件。
func (k *Kernel) SetUnloadBlocker(at int64, docID string, v bool) error {
	return k.mutateDoc(at, docID, func(d *document) { d.flags.unloadBlocker = v })
}

// SetExclusiveResource 设置"持有独占资源"条件。
func (k *Kernel) SetExclusiveResource(at int64, docID string, v bool) error {
	return k.mutateDoc(at, docID, func(d *document) { d.flags.exclusiveResource = v })
}

// SetUncacheable 设置"被标记为不可缓存"条件（如远程标记）。
func (k *Kernel) SetUncacheable(at int64, docID string, v bool) error {
	return k.mutateDoc(at, docID, func(d *document) { d.flags.markedUncacheable = v })
}

// mutateDoc 是文档状态变化的公共路径，按规定的拒绝次序检查：
// 参数非法 -> 时钟回退 -> 文档不存在 -> 状态不允许。
// 条件变化后若缓存中的文档不再满足资格，立即驱逐。
func (k *Kernel) mutateDoc(at int64, docID string, fn func(*document)) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if docID == "" {
		return errf(KindInvalidArgument, "empty document id")
	}
	if at < k.now {
		return errf(KindClockRollback, "op time %d before kernel time %d", at, k.now)
	}
	if at > k.now {
		k.now = at
		k.evictExpiredLocked()
	}
	d := k.docs[docID]
	if d == nil {
		return errf(KindDocumentNotFound, "document %q", docID)
	}
	if d.status == statusUnloaded {
		return errf(KindInvalidState, "document %q is unloaded", docID)
	}
	fn(d)
	if d.status == statusCached && !d.flags.eligible() {
		k.evictLocked(d)
	}
	return nil
}

// ---- 内部协作原语 ----

func (k *Kernel) allocDocLocked() string {
	k.docSeq++
	id := fmt.Sprintf("D%d", k.docSeq)
	k.docs[id] = &document{id: id, status: statusActive, heapIndex: -1}
	return id
}

// truncateLocked 截断当前位置之后的条目；失去全部引用的缓存文档立即驱逐。
func (k *Kernel) truncateLocked() {
	for i := k.pos + 1; i < len(k.entries); i++ {
		id := k.entries[i].DocID
		if k.ix.remove(id, i) {
			if d := k.docs[id]; d != nil && d.status == statusCached {
				k.evictLocked(d)
			}
		}
	}
	k.entries = k.entries[:k.pos+1]
}

// leaveCurrentLocked 在跨文档离开时按资格判定：入缓存或卸载。
func (k *Kernel) leaveCurrentLocked() {
	d := k.docs[k.entries[k.pos].DocID]
	if d == nil || d.status != statusActive {
		return
	}
	if k.capacity > 0 && d.flags.eligible() {
		d.status = statusCached
		d.enteredAt = k.now
		k.insertSeq++
		d.order = k.insertSeq
		k.cache.insert(d)
	} else {
		d.status = statusUnloaded
	}
}

// reloadLocked 为 oldID 分配新文档标识，并把同文档的所有条目一并更新。
// 借助 entryIndex，开销只与共享该标识的条目数有关，与历史总长无关。
func (k *Kernel) reloadLocked(oldID string) {
	newID := k.allocDocLocked()
	set := k.ix[oldID]
	for i := range set {
		k.entries[i].DocID = newID
	}
	delete(k.ix, oldID)
	k.ix[newID] = set
	if old := k.docs[oldID]; old != nil && old.status == statusActive {
		old.status = statusUnloaded
	}
}

func (k *Kernel) evictLocked(d *document) {
	k.cache.remove(d)
	d.status = statusUnloaded
}

func (k *Kernel) enforceCapacityLocked() {
	for k.cache.len() > k.capacity {
		k.cache.evictEarliest()
	}
}

func (k *Kernel) evictExpiredLocked() {
	k.cache.evictExpired(k.now, k.ttl, nil)
}

// ---- 只读观察口 ----

// Current 返回当前条目；历史为空时 ok 为 false。
func (k *Kernel) Current() (Entry, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.pos < 0 {
		return Entry{}, false
	}
	return k.entries[k.pos], true
}

// Entries 返回历史条目列表的副本。
func (k *Kernel) Entries() []Entry {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]Entry, len(k.entries))
	copy(out, k.entries)
	return out
}

// Position 返回当前位置下标。
func (k *Kernel) Position() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.pos
}

// Now 返回内核时钟。
func (k *Kernel) Now() int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.now
}

// DocStatus 返回文档状态；文档不存在时 ok 为 false。
func (k *Kernel) DocStatus(docID string) (docStatus, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	d := k.docs[docID]
	if d == nil {
		return 0, false
	}
	return d.status, true
}

// IsCached 报告文档是否在缓存中。
func (k *Kernel) IsCached(docID string) bool {
	s, ok := k.DocStatus(docID)
	return ok && s == statusCached
}

// CachedDocIDs 返回缓存中文档标识的有序列表。
func (k *Kernel) CachedDocIDs() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]string, 0, k.cache.len())
	for _, d := range k.cache.h {
		out = append(out, d.id)
	}
	sort.Strings(out)
	return out
}

// CheckInvariants 校验三条串行不变量，供测试与调试使用：
// 缓存文档数不超过上限；每个缓存文档都被至少一个条目引用；当前条目的文档不在缓存中。
func (k *Kernel) CheckInvariants() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cache.len() > k.capacity {
		return fmt.Errorf("cache size %d exceeds capacity %d", k.cache.len(), k.capacity)
	}
	for _, d := range k.cache.h {
		if d.status != statusCached {
			return fmt.Errorf("document %s in heap but status %s", d.id, d.status)
		}
		if k.ix.refcount(d.id) == 0 {
			return fmt.Errorf("cached document %s has no referencing entry", d.id)
		}
	}
	if k.pos >= 0 {
		cur := k.docs[k.entries[k.pos].DocID]
		if cur != nil && cur.status == statusCached {
			return fmt.Errorf("current document %s is cached", cur.id)
		}
	}
	for i, e := range k.entries {
		set := k.ix[e.DocID]
		if _, ok := set[i]; !ok {
			return fmt.Errorf("entry %d (doc %s) missing from index", i, e.DocID)
		}
	}
	for i := range k.cache.h {
		if l := 2*i + 1; l < len(k.cache.h) && k.cache.h.Less(l, i) {
			return fmt.Errorf("heap property violated at %d", i)
		}
	}
	return nil
}
