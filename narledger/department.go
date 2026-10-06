package narledger

import "container/heap"

// department 维护科室级别的 O(1) 判定计数器与逾期/差额索引。
//
// 逾期的出现/解除随时间自然体现：dueHeap 只保存当前仍处于 OPEN 的单据，
// 按结清期限排序。单据结清/转差额时通过 duePos 在 O(log n) 内立即摘除，
// 因此每张单据入堆、出堆各一次；refresh 只弹出堆顶已到点的单据并计入
// overdue 集合，锁定判定的摊还开销不随该科室历史单据总数增长。
type department struct {
	openCount   int              // 当前 OPEN（未结清）单据数；差额单据不计入
	overdue     map[string]int64 // 逾期 OPEN 单据 id -> deadline
	discrepancy map[string]struct{}
	dueHeap     *dueMinHeap
	duePos      map[string]int // OPEN 单据 id -> 堆下标
}

type dueItem struct {
	id       string
	deadline int64
}

type dueMinHeap []dueItem

func (h dueMinHeap) Len() int { return len(h) }
func (h dueMinHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].id < h[j].id
}
func (h dueMinHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *dueMinHeap) Push(x any)   { *h = append(*h, x.(dueItem)) }
func (h *dueMinHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func newDepartment() *department {
	return &department{
		overdue:     make(map[string]int64),
		discrepancy: make(map[string]struct{}),
		dueHeap:     &dueMinHeap{},
		duePos:      make(map[string]int),
	}
}

// noteOpen 新领用：计入 OPEN 与期限堆。
func (d *department) noteOpen(id string, deadline int64) {
	d.openCount++
	heap.Push(d.dueHeap, dueItem{id: id, deadline: deadline})
	d.duePos[id] = d.dueHeap.Len() - 1
}

// refresh 把在 now 已严格超过期限的 OPEN 堆顶移入 overdue 集合。
func (d *department) refresh(now int64, alive func(id string) bool) {
	for d.dueHeap.Len() > 0 {
		top := (*d.dueHeap)[0]
		if top.deadline >= now {
			return // now <= deadline 仍属期内
		}
		heap.Pop(d.dueHeap)
		delete(d.duePos, top.id)
		if alive(top.id) {
			d.overdue[top.id] = top.deadline
		}
	}
}

// settleOpen 单据离开 OPEN 状态（结清/转差额）时立即从期限堆摘除。
func (d *department) settleOpen(id string, toDiscrepancy bool) {
	if d.openCount > 0 {
		d.openCount--
	}
	if toDiscrepancy {
		d.discrepancy[id] = struct{}{}
	}
	d.removeOpen(id)
}

// removeOpen 从 overdue 集合或期限堆中摘除一张已离开 OPEN 的单据。
func (d *department) removeOpen(id string) {
	if _, isOverdue := d.overdue[id]; isOverdue {
		delete(d.overdue, id)
		return
	}
	pos, ok := d.duePos[id]
	if !ok {
		return
	}
	if pos < 0 || pos >= d.dueHeap.Len() || (*d.dueHeap)[pos].id != id {
		// 并发同刻操作下 refresh 已把该单据弹出（入 overdue 后又被另一路摘除），
		// 位置映射惰性失效。
		delete(d.duePos, id)
		return
	}
	heap.Remove(d.dueHeap, pos)
	delete(d.duePos, id)
}

// resolveDiscrepancy 差额处理完成。
func (d *department) resolveDiscrepancy(id string) { delete(d.discrepancy, id) }

func (d *department) locked() bool {
	return len(d.overdue) > 0 || len(d.discrepancy) > 0
}

// deptBook 科室账册。
type deptBook struct{ depts map[string]*department }

func newDeptBook() *deptBook { return &deptBook{depts: make(map[string]*department)} }

func (b *deptBook) get(name string) *department {
	d := b.depts[name]
	if d == nil {
		d = newDepartment()
		b.depts[name] = d
	}
	return d
}
