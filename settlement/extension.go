package settlement

import "container/heap"

// 延期可见性语义（可精确复现）：
//
// 时刻 t 的提交能看到一次延期，当且仅当 grantAt < t <= revokeAt，
// revokeAt=0 表示尚未撤销。
//
//   - 延期在“授予时刻之后”才生效；同刻授予再提交互不可见。
//   - 撤销只影响“此后”的版本；撤销时刻同刻的提交仍可见。
//   - 多次延期取延长时长的最大值，而非求和。
//
// 数据结构（开销证明见 DESIGN.md）：
//
//   - 全部授予/撤销事件按触发时刻保存在各自有序队列中，每个事件
//     一生只被一个单调前移的游标消费一次。
//   - advance(t) 把所有“时刻 < t”的事件以同一时刻为一个原子批次
//     应用到两个懒删除最大堆：visible（当前可见）与 alive（全部
//     未撤销，供封顶检查）。
//   - 一旦推进到 t，结果即固化；EffectiveAt 是只读查询。提交时
//     对每个成员的多次取值来自同一个已固化状态，天然一致。
//
// 全部 E 次授予/撤销的总代价为 O(E log E)；单次取值为堆顶 O(1)，
// 不随该作业延期总数线性增长。

type extRec struct {
	id       int
	grantAt  int
	revokeAt int
	amount   int
}

type timedEvent struct {
	at int
	id int
}

// extHeap 为延长时长的最大堆；与配对死堆按值做多重集抵消。
type extHeap []int

func (h extHeap) Len() int           { return len(h) }
func (h extHeap) Less(i, j int) bool { return h[i] > h[j] }
func (h extHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *extHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *extHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

var _ heap.Interface = (*extHeap)(nil)

// extIndex 维护一个成员（或一个小组）在各时刻可见延期的最大值。
type extIndex struct {
	nextID int

	records  map[int]*extRec
	grantQ   []timedEvent
	revokeQ  []timedEvent
	visible  extHeap
	visDead  extHeap
	alive    extHeap
	aliveD   extHeap
	cur      int
	ready    bool // visible/visDead 已反映时刻 cur 的状态
	curValue int
}

func newExtIndex() *extIndex {
	return &extIndex{nextID: 1, records: map[int]*extRec{}}
}

// insertEvent 按 (at,id) 升序把事件插入队列（now 单调不降，通常
// 仅需越过队尾同刻事件）。
func insertEvent(q *[]timedEvent, ev timedEvent) {
	pos := len(*q)
	for pos > 0 {
		last := (*q)[pos-1]
		if last.at < ev.at || (last.at == ev.at && last.id < ev.id) {
			break
		}
		pos--
	}
	*q = append(*q, timedEvent{})
	copy((*q)[pos+1:], (*q)[pos:])
	(*q)[pos] = ev
}

// grantWithID 以调用方分配的作业内全局 ID 记录一次延期。
func (x *extIndex) grantWithID(id, now, amount int) int {
	if id >= x.nextID {
		x.nextID = id + 1
	}
	x.records[id] = &extRec{id: id, grantAt: now, amount: amount}
	insertEvent(&x.grantQ, timedEvent{at: now, id: id})
	heap.Push(&x.alive, amount)
	x.ready = false
	return id
}

// Revoke 登记撤销事件。不存在或已撤销返回 false。
func (x *extIndex) Revoke(revokeAt, extID int) bool {
	r, ok := x.records[extID]
	if !ok || r.revokeAt != 0 {
		return false
	}
	r.revokeAt = revokeAt
	insertEvent(&x.revokeQ, timedEvent{at: revokeAt, id: extID})
	x.ready = false
	return true
}

// prune 弹出 value/dead 两侧相等的堆顶（多重集懒删除）。
func prune(value, dead *extHeap) {
	for value.Len() > 0 && dead.Len() > 0 && (*value)[0] == (*dead)[0] {
		heap.Pop(value)
		heap.Pop(dead)
	}
}

// advance 固化到时刻 t：应用所有触发时刻严格小于 t 的事件。
// 同一时刻按“授予批次后撤销批次”原子处理，因此同刻授予并撤销的
// 延期对同刻提交不可见；同刻撤销的既有延期对同刻提交仍可见
// （其撤销事件要等到下一刻才被消费）。
func (x *extIndex) advance(t int) {
	if t <= x.cur {
		return
	}
	x.cur = t
	for len(x.grantQ) > 0 || len(x.revokeQ) > 0 {
		ts, has := nextBatchTime(x.grantQ, x.revokeQ, t)
		if !has {
			break
		}
		// 批次一：授予。同刻已撤销者不入可见堆（alive 死项在批次二
		// 统一配对）。
		for len(x.grantQ) > 0 && x.grantQ[0].at == ts {
			ev := x.grantQ[0]
			x.grantQ = x.grantQ[1:]
			r := x.records[ev.id]
			if r.revokeAt == ts {
				continue
			}
			heap.Push(&x.visible, r.amount)
		}
		// 批次二：撤销。
		for len(x.revokeQ) > 0 && x.revokeQ[0].at == ts {
			ev := x.revokeQ[0]
			x.revokeQ = x.revokeQ[1:]
			r := x.records[ev.id]
			heap.Push(&x.aliveD, r.amount)
			// 仅当授予在更早时刻已进入 visible 时才需注销可见堆；
			// 同刻授予+撤销的记录在批次一已跳过，不能再入死堆。
			if r.grantAt < ts {
				heap.Push(&x.visDead, r.amount)
			}
		}
	}
	prune(&x.visible, &x.visDead)
	prune(&x.alive, &x.aliveD)
	x.curValue = 0
	if x.visible.Len() > 0 {
		x.curValue = x.visible[0]
	}
	x.ready = true
}

func nextBatchTime(g, r []timedEvent, limit int) (int, bool) {
	best := 0
	found := false
	if len(g) > 0 && g[0].at < limit {
		best, found = g[0].at, true
	}
	if len(r) > 0 && r[0].at < limit && (!found || r[0].at < best) {
		best, found = r[0].at, true
	}
	return best, found
}

// EffectiveAt 返回时刻 t 的提交所看到延期的最大延长时长。
// 调用方（提交路径）在同一事务中先 advance(now) 一次，随后对小组
// 与每个成员的读取都命中已固化状态，故本方法为只读 O(1)。
func (x *extIndex) EffectiveAt(t int) int {
	if !x.ready || x.cur < t {
		x.advance(t)
	}
	return x.curValue
}

// maxAlive 返回当前全部未撤销授予（含尚未激活者）的最大延长时长，
// 用于授予前封顶检查。
func (x *extIndex) maxAlive() int {
	prune(&x.alive, &x.aliveD)
	if x.alive.Len() == 0 {
		return 0
	}
	return x.alive[0]
}

// GrantExtension 向“个人”或“小组”授予一次延期。
//
//   - isGroup=false：目标 targetID 为学生 ID，延期只影响该成员的取档
//     （个人作业则直接影响其个人主体）。
//   - isGroup=true：目标 targetID 为小组 ID，延期对全体在组成员生效。
//
// 同一目标多次延期时有效截止 = Deadline + max(各次延长)，而非求和。
// 授予不得使有效截止（含本次与此前全部未撤销延期）晚于硬性关闭时刻；
// 超出报 ErrExtensionPastClose 且可区分。授予时刻之后的提交才受影响。
func (e *Engine) GrantExtension(assignmentID, targetID string, isGroup bool, now, amount int) (int, error) {
	if targetID == "" || amount <= 0 {
		return 0, ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rollback(now); err != nil {
		return 0, err
	}
	a, err := e.lookupAssignment(assignmentID)
	if err != nil {
		return 0, err
	}
	if a.settled {
		return 0, ErrSettled
	}
	var idx *extIndex
	if isGroup {
		if !a.cfg.IsGroup {
			return 0, classified(ErrInvalidParam, "assignment is not a group assignment")
		}
		g, ok := a.groups[targetID]
		if !ok {
			return 0, classified(ErrNotFound, "group not found: "+targetID)
		}
		idx = a.groupExt[g.id]
	} else {
		p, ok := a.people[targetID]
		if !ok {
			return 0, classified(ErrNotFound, "person not found: "+targetID)
		}
		idx = &p.personal
	}
	if now > a.cfg.HardClose {
		return 0, ErrAfterHardClose
	}
	// 封顶：取“当前全部未撤销延期（含尚未激活者）”与本次延长的最大值。
	if cand := amount; cand > idx.maxAlive() {
		if a.cfg.Deadline+cand > a.cfg.HardClose {
			return 0, ErrExtensionPastClose
		}
	}
	if err := e.checkClock(now); err != nil {
		return 0, err
	}
	id := a.nextExtID
	a.nextExtID++
	return idx.grantWithID(id, now, amount), nil
}

// RevokeExtension 撤销一次延期。撤销只影响此后的版本：撤销时刻的提交
// 仍按旧有效截止判定。isGroup 必须与授予时一致；延期不存在或已撤销
// 报 ErrNotFound。
func (e *Engine) RevokeExtension(assignmentID, targetID string, isGroup bool, now, extID int) error {
	if targetID == "" || extID <= 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rollback(now); err != nil {
		return err
	}
	a, err := e.lookupAssignment(assignmentID)
	if err != nil {
		return err
	}
	if a.settled {
		return ErrSettled
	}
	var idx *extIndex
	if isGroup {
		if !a.cfg.IsGroup {
			return classified(ErrInvalidParam, "assignment is not a group assignment")
		}
		g, ok := a.groups[targetID]
		if !ok {
			return classified(ErrNotFound, "group not found: "+targetID)
		}
		idx = a.groupExt[g.id]
	} else {
		p, ok := a.people[targetID]
		if !ok {
			return classified(ErrNotFound, "person not found: "+targetID)
		}
		idx = &p.personal
	}
	if now > a.cfg.HardClose {
		return ErrAfterHardClose
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if !idx.Revoke(now, extID) {
		return classified(ErrNotFound, "extension not found or already revoked")
	}
	return nil
}
