// Package fkpend 维护外键未就绪行的挂起队列、超时死信与 BFS 释放原语。
package fkpend

import (
	"container/heap"
	"sync"

	"ontology/idmap"
)

// Entry 是一个挂起行的载荷。
type Entry struct {
	Key idmap.Key
	A   int64
	B   int64
}

// entry 是挂起行的内部记录：除载荷外保留到达序 aseq、到达 now、
// 当前等待键，以及它在等待桶堆与到期堆中的位置。
type entry struct {
	Entry
	aseq     int
	arrive   int64
	wait     idmap.Key
	waitIdx  int // 在 waits[wait] 堆中的下标，-1 表示不在等待桶
	expireAt int64
	expIdx   int // 在 dueHeap 中的下标
}

// waitHeap 是单个等待键桶，按 aseq 升序的最小堆。
type waitHeap []*entry

func (h waitHeap) Len() int           { return len(h) }
func (h waitHeap) Less(i, j int) bool { return h[i].aseq < h[j].aseq }
func (h waitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].waitIdx = i; h[j].waitIdx = j }
func (h *waitHeap) Push(x any)        { e := x.(*entry); e.waitIdx = len(*h); *h = append(*h, e) }
func (h *waitHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.waitIdx = -1
	*h = old[:n-1]
	return e
}

// dueHeap 按到期时间（arrive+T）排序；同到期时间按 aseq 升序，
// 使到期扫描与死信次序均为「先到期、同到期按到达序」，即按 aseq 升序。
type dueHeap []*entry

func (h dueHeap) Len() int { return len(h) }
func (h dueHeap) Less(i, j int) bool {
	if h[i].expireAt != h[j].expireAt {
		return h[i].expireAt < h[j].expireAt
	}
	return h[i].aseq < h[j].aseq
}
func (h dueHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i]; h[i].expIdx = i; h[j].expIdx = j }
func (h *dueHeap) Push(x any)   { e := x.(*entry); e.expIdx = len(*h); *h = append(*h, e) }
func (h *dueHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.expIdx = -1
	*h = old[:n-1]
	return e
}

// New 创建容量为 cap 的挂起队列（骨架）。
//
// Queue 是挂起队列。
type Queue struct {
	mu        sync.Mutex
	t         int64
	cap       int
	nextSeq   int
	byKey     map[idmap.Key]*entry
	waits     map[idmap.Key]*waitHeap
	due       dueHeap
	dead      []idmap.Key
	inspected int // 释放时从等待桶检视（弹出）的挂起项数
}

// New 创建超时 t、容量 cap 的挂起队列。
func New(capacity int, t int64) *Queue {
	return &Queue{
		t:     t,
		cap:   capacity,
		byKey: make(map[idmap.Key]*entry),
		waits: make(map[idmap.Key]*waitHeap),
		due:   make(dueHeap, 0),
	}
}

// Len 返回当前挂起行数。
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.byKey)
}

// Pending 返回某键是否处于挂起状态。
func (q *Queue) Pending(k idmap.Key) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.byKey[k]
	return ok
}

// Inspected 返回自上次重置以来释放流程从等待桶检视的挂起项数（非导出计数器的测试视图）。
func (q *Queue) Inspected() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.inspected
}

// ResetInspected 清零检视计数。
func (q *Queue) ResetInspected() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inspected = 0
}

// Dead 返回按进入次序排列的死信键副本。
func (q *Queue) Dead() []idmap.Key {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]idmap.Key, len(q.dead))
	copy(out, q.dead)
	return out
}

// Add 新增一个挂起行（调用方须保证键当前未挂起）。分配新的 aseq。
func (q *Queue) Add(e Entry, wait idmap.Key, now int64) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.addLocked(e, wait, now)
}

func (q *Queue) addLocked(e Entry, wait idmap.Key, now int64) int {
	q.nextSeq++
	en := &entry{
		Entry:    e,
		aseq:     q.nextSeq,
		arrive:   now,
		wait:     wait,
		waitIdx:  -1,
		expireAt: now + q.t,
		expIdx:   -1,
	}
	q.byKey[e.Key] = en
	heap.Push(&q.due, en)
	q.pushWait(en)
	return en.aseq
}

// Replace 用新载荷覆盖已有挂起行，保留原 aseq 与到达 now，并重建等待键。
func (q *Queue) Replace(e Entry, wait idmap.Key) {
	q.mu.Lock()
	defer q.mu.Unlock()
	en := q.byKey[e.Key]
	en.Entry = e
	q.rewaitLocked(en, wait)
}

// rewaitLocked 把挂起行从旧等待桶移到新等待桶；同键则不动。
func (q *Queue) rewaitLocked(en *entry, wait idmap.Key) {
	if en.wait == wait {
		return
	}
	if h := q.waits[en.wait]; h != nil {
		heap.Remove(h, en.waitIdx)
		if h.Len() == 0 {
			delete(q.waits, en.wait)
		}
	}
	en.wait = wait
	q.pushWait(en)
}

func (q *Queue) pushWait(en *entry) {
	h := q.waits[en.wait]
	if h == nil {
		h = &waitHeap{}
		q.waits[en.wait] = h
	}
	heap.Push(h, en)
}

// Get 取出挂起行载荷与到达信息；未挂起时 ok 为 false。
func (q *Queue) Get(k idmap.Key) (e Entry, aseq int, arrive int64, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	en, exists := q.byKey[k]
	if !exists {
		return Entry{}, 0, 0, false
	}
	return en.Entry, en.aseq, en.arrive, true
}

// PendingInfo 是挂起行的测试视图：携带到达序、到达 now 与当前等待键。
type PendingInfo struct {
	Entry  Entry
	Aseq   int
	Arrive int64
	Wait   idmap.Key
}

// PendingAll 返回全部挂起行的副本（无序，调用方按需排序），仅供测试与对拍。
func (q *Queue) PendingAll() []PendingInfo {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]PendingInfo, 0, len(q.byKey))
	for _, en := range q.byKey {
		out = append(out, PendingInfo{
			Entry:  en.Entry,
			Aseq:   en.aseq,
			Arrive: en.arrive,
			Wait:   en.wait,
		})
	}
	return out
}

// RemovePending 删除某键的挂起行；返回此前是否存在（DroppedPending 判定用，
// 须在到期处理之后调用）。
func (q *Queue) RemovePending(k idmap.Key) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.removeLocked(k)
}

func (q *Queue) removeLocked(k idmap.Key) bool {
	en, ok := q.byKey[k]
	if !ok {
		return false
	}
	delete(q.byKey, k)
	heap.Remove(&q.due, en.expIdx)
	if h := q.waits[en.wait]; h != nil {
		heap.Remove(h, en.waitIdx)
		if h.Len() == 0 {
			delete(q.waits, en.wait)
		}
	}
	return true
}

// ExpiringCount 在不修改任何状态的前提下，统计 now 时刻到期的挂起行数，
// 供 ErrFull 在「到期处理之前」完成容量判定。
func (q *Queue) ExpiringCount(now int64) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for n < q.due.Len() && q.due[n].expireAt <= now {
		n++
	}
	return n
}

// Expire 把 now 时刻到期的挂起行按 aseq 升序移入死信，返回移入数量。
func (q *Queue) Expire(now int64) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for q.due.Len() > 0 {
		en := q.due[0]
		if en.expireAt > now {
			break
		}
		// 到期堆同 expireAt 内按 aseq 升序，故弹出序即 aseq 升序。
		heap.Pop(&q.due)
		delete(q.byKey, en.Entry.Key)
		if h := q.waits[en.wait]; h != nil {
			heap.Remove(h, en.waitIdx)
			if h.Len() == 0 {
				delete(q.waits, en.wait)
			}
		}
		q.dead = append(q.dead, en.Entry.Key)
		n++
	}
	return n
}

// Release 以触发落库的键 root 为起点执行一次完整的广度优先释放。
//
// 每检视一个等待桶堆顶（aseq 最小）即计入 inspected：弹出后经 fn 重新判定，
// fn 返回新等待键的零值表示就绪落库，同时把以该键为等待键的挂起项
// 按 aseq 升序追加到工作队列队尾；非零表示仍不就绪，挂起行改等新键，
// 保留 aseq 与到达 now。
func (q *Queue) Release(root idmap.Key, fn func(e Entry, aseq int, arrive int64) idmap.Key) {
	q.mu.Lock()
	defer q.mu.Unlock()
	work := []idmap.Key{root}
	for len(work) > 0 {
		k := work[0]
		work = work[1:]
		h := q.waits[k]
		var rewait []*entry
		for h != nil && h.Len() > 0 {
			q.inspected++
			en := heap.Pop(h).(*entry)
			nw := fn(en.Entry, en.aseq, en.arrive)
			if nw == (idmap.Key{}) {
				// 就绪落库：从挂起索引移除（已弹出等待桶），其等待桶待队尾展开。
				delete(q.byKey, en.Entry.Key)
				heap.Remove(&q.due, en.expIdx)
				work = append(work, en.Entry.Key)
			} else {
				// 推迟到本桶排空后再回插：新等待键可能就是当前键，
				// 立即回插会在同一轮排空里被重复检视。
				en.wait = nw
				rewait = append(rewait, en)
			}
		}
		backToK := false
		for _, en := range rewait {
			if en.wait == k {
				backToK = true
			}
			q.pushWait(en)
		}
		if h != nil && !backToK {
			delete(q.waits, k)
		}
	}
}
