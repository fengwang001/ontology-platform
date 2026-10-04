// Package fkpend 管理外键未就绪行的挂起队列：容量限制、超时移出、
// 按等待键索引，以及由落库事件触发的广度优先释放。
//
// 不是并发安全的，由调用方串行化。
package fkpend

import "sort"

// Key 是源侧键：分片号 + 表 kind + 源 id。
type Key struct {
	Shard int
	Kind  int
	ID    int64
}

// Row 是一行挂起记录：源引用 a、b、全局到达序号 aseq、到达 now 与当前等待键。
type Row struct {
	Key     Key
	A, B    int64
	Aseq    int64
	Arrival int64
	Wait    Key
}

// Queue 是挂起队列。
type Queue struct {
	limit   int
	timeout int64
	aseq    int64
	rows    map[Key]*Row
	byWait  map[Key]map[Key]*Row
	// inspected 统计释放过程中检视的挂起行数；每次检视都导致一次释放或
	// 一次重新挂起，故 inspected == 释放数 + 重新挂起数，与队列总长无关。
	inspected int64
}

// New 返回容量为 limit、挂起超时为 timeout 的队列。
func New(limit int, timeout int64) *Queue {
	return &Queue{
		limit:   limit,
		timeout: timeout,
		rows:    make(map[Key]*Row),
		byWait:  make(map[Key]map[Key]*Row),
	}
}

// Len 返回当前挂起行数。
func (q *Queue) Len() int {
	return len(q.rows)
}

// Full 报告新增一行是否会超出容量。
func (q *Queue) Full() bool {
	return len(q.rows) >= q.limit
}

// Has 报告键是否有挂起行。
func (q *Queue) Has(k Key) bool {
	_, ok := q.rows[k]
	return ok
}

// Row 返回键的挂起行副本；ok 为 false 表示无挂起行。
func (q *Queue) Row(k Key) (Row, bool) {
	r, ok := q.rows[k]
	if !ok {
		return Row{}, false
	}
	return *r, true
}

// Add 新增挂起行，分配全局递增的 aseq 并记录到达 now。调用方须保证键不存在且队列未满。
func (q *Queue) Add(k Key, a, b int64, wait Key, now int64) {
	q.aseq++
	r := &Row{Key: k, A: a, B: b, Aseq: q.aseq, Arrival: now, Wait: wait}
	q.rows[k] = r
	q.index(r)
}

// Replace 用新内容替换已有挂起行，保留原 aseq 与到达 now。
func (q *Queue) Replace(k Key, a, b int64, wait Key) {
	r := q.rows[k]
	q.deindex(r)
	r.A, r.B, r.Wait = a, b, wait
	q.index(r)
}

// Remove 删除键的挂起行（落库丢弃、Delete 删除或到期移出时使用）。
func (q *Queue) Remove(k Key) bool {
	r, ok := q.rows[k]
	if !ok {
		return false
	}
	q.deindex(r)
	delete(q.rows, k)
	return true
}

// Expire 把「now 减到达 now 不小于超时」的挂起行按 aseq 升序移出并返回。
func (q *Queue) Expire(now int64) []Row {
	var out []*Row
	for _, r := range q.rows {
		if now-r.Arrival >= q.timeout {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Aseq < out[j].Aseq })
	res := make([]Row, 0, len(out))
	for _, r := range out {
		res = append(res, *r)
		q.deindex(r)
		delete(q.rows, r.Key)
	}
	return res
}

// Release 从 trigger（刚落库的键）开始广度优先释放：
// 把以它为等待键的挂起行按 aseq 升序入 FIFO 工作队列，队头逐个交给 judge
// 重新判定；judge 返回 applied 时该行落库（从挂起队列删除），并把以它为
// 等待键的行按 aseq 升序追加到队尾；否则该行改等 judge 返回的新等待键，
// 保留 aseq 与到达 now。
func (q *Queue) Release(trigger Key, judge func(r *Row) (applied bool, wait Key)) {
	work := q.detach(trigger)
	for len(work) > 0 {
		r := work[0]
		work = work[1:]
		q.inspected++
		applied, wait := judge(r)
		if applied {
			delete(q.rows, r.Key)
			work = append(work, q.detach(r.Key)...)
		} else {
			r.Wait = wait
			q.index(r)
		}
	}
}

// detach 摘下以 w 为等待键的全部挂起行（从等待索引移除，仍留在队列中），
// 按 aseq 升序返回。
func (q *Queue) detach(w Key) []*Row {
	set := q.byWait[w]
	if len(set) == 0 {
		return nil
	}
	delete(q.byWait, w)
	out := make([]*Row, 0, len(set))
	for _, r := range set {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Aseq < out[j].Aseq })
	return out
}

func (q *Queue) index(r *Row) {
	set := q.byWait[r.Wait]
	if set == nil {
		set = make(map[Key]*Row)
		q.byWait[r.Wait] = set
	}
	set[r.Key] = r
}

func (q *Queue) deindex(r *Row) {
	if set := q.byWait[r.Wait]; set != nil {
		delete(set, r.Key)
		if len(set) == 0 {
			delete(q.byWait, r.Wait)
		}
	}
}
