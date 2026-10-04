package task

import "ontology/slot"

// Kind 为任务类型。
type Kind int

const (
	Regular Kind = iota
	Urgent
)

// Status 为任务生命周期状态。
type Status int

const (
	Open Status = iota
	Done
	Cancelled
)

// Task 是一条补货任务记录。
type Task struct {
	ID     int64
	SKU    string
	Loc    string
	Qty    int64
	Kind   Kind
	Status Status

	// 库位内两条双链的前后指针（同类型任务按 ID 升序）。
	rPrev, rNext *Task
	uPrev, uNext *Task
}

// Manager 管理任务号、任务表与各库位的常规/紧急双链。
type Manager struct {
	store *slot.Store
	seq   int64
	tasks map[int64]*Task
	chain map[string]*chains
}

// chains 为一个库位的常规、紧急双链。
type chains struct {
	rHead, rTail *Task
	uHead, uTail *Task
}

// NewManager 创建任务管理器。
func NewManager(st *slot.Store) *Manager {
	return &Manager{store: st, tasks: map[int64]*Task{}, chain: map[string]*chains{}}
}

// Create 取新任务号并登记任务（已完成全部业务校验后调用）。
// 调用方负责先校验储备可用性；本方法增量登记在途占用（只登记，不扣储备）。
func (m *Manager) Create(loc, sku string, qty int64, kind Kind) *Task {
	m.seq++
	t := &Task{ID: m.seq, SKU: sku, Loc: loc, Qty: qty, Kind: kind, Status: Open}
	m.tasks[t.ID] = t
	ch := m.locChain(loc)
	if kind == Regular {
		appendTail(&ch.rHead, &ch.rTail, t)
	} else {
		insertUrgent(&ch.uHead, &ch.uTail, t)
	}
	sl := m.store.Get(loc) // 库位记录此前已触碰，touch 去重不会重复计数
	m.store.ReserveOpen(sl, qty)
	m.store.TouchTask(t.ID)
	return t
}

// Get 返回任务（不存在为 nil），触碰 1 条任务记录。
func (m *Manager) Get(id int64) *Task {
	t := m.tasks[id]
	if t != nil {
		m.store.TouchTask(id)
	}
	return t
}

// UpgradeRegular 把 loc 的常规任务按 ID 升序升级，直到 onHand+紧急量 >= need。
// 返回被升级的任务。触碰即升级的任务记录。
func (m *Manager) UpgradeRegular(loc string, onHand, need int64) []*Task {
	ch := m.chain[loc]
	if ch == nil {
		return nil
	}
	urgent := int64(0)
	var upgraded []*Task
	for ch.rHead != nil {
		if onHand+urgent >= need {
			break
		}
		t := ch.rHead
		urgent += t.Qty
		detachRegular(ch, t)
		insertUrgent(&ch.uHead, &ch.uTail, t)
		t.Kind = Urgent
		upgraded = append(upgraded, t)
		m.store.TouchTask(t.ID)
	}
	return upgraded
}

// Remove 把任务从库位双链摘除（Confirm/Cancel 提交点调用）。
// 任务记录保留在任务表中并带终态，以便区分「不存在」与「已完成/已取消」。
func (m *Manager) Remove(t *Task) {
	ch := m.chain[t.Loc]
	if ch != nil {
		if t.Kind == Regular {
			detachRegular(ch, t)
		} else {
			detachUrgent(ch, t)
		}
	}
}

// OpenTasks 快照全部未完成任务：紧急在前，同类按 ID 升序。
func (m *Manager) OpenTasks() []*Task {
	var out []*Task
	for _, ch := range m.chain {
		for t := ch.uHead; t != nil; t = t.uNext {
			out = append(out, t)
		}
		for t := ch.rHead; t != nil; t = t.rNext {
			out = append(out, t)
		}
	}
	sortTasks(out)
	return out
}

func (m *Manager) locChain(loc string) *chains {
	ch, ok := m.chain[loc]
	if !ok {
		ch = &chains{}
		m.chain[loc] = ch
	}
	return ch
}

// UrgentQty 返回某库位当前紧急任务量之和（沿紧急链累加，不触碰）。
func (m *Manager) UrgentQty(loc string) int64 {
	ch := m.chain[loc]
	if ch == nil {
		return 0
	}
	var sum int64
	for t := ch.uHead; t != nil; t = t.uNext {
		sum += t.Qty
	}
	return sum
}

// ---- 双链原语：同库位同类型任务始终按 ID 升序 ----

func appendTail(head, tail **Task, t *Task) {
	t.rPrev, t.rNext = nil, nil
	if *tail == nil {
		*head, *tail = t, t
		return
	}
	t.rPrev = *tail
	(*tail).rNext = t
	*tail = t
}

// insertUrgent 按 ID 有序插入紧急链（升级场景下新 ID 必然最大，退化为尾插）。
func insertUrgent(head, tail **Task, t *Task) {
	t.uPrev, t.uNext = nil, nil
	if *head == nil {
		*head, *tail = t, t
		return
	}
	prev := *tail
	for prev != nil && prev.ID > t.ID {
		prev = prev.uPrev
	}
	if prev == nil {
		t.uNext = *head
		(*head).uPrev = t
		*head = t
		return
	}
	t.uPrev = prev
	t.uNext = prev.uNext
	prev.uNext = t
	if t.uNext != nil {
		t.uNext.uPrev = t
	} else {
		*tail = t
	}
}

func detachRegular(ch *chains, t *Task) {
	if t.rPrev != nil {
		t.rPrev.rNext = t.rNext
	} else if ch.rHead == t {
		ch.rHead = t.rNext
	}
	if t.rNext != nil {
		t.rNext.rPrev = t.rPrev
	} else if ch.rTail == t {
		ch.rTail = t.rPrev
	}
	t.rPrev, t.rNext = nil, nil
}

func detachUrgent(ch *chains, t *Task) {
	if t.uPrev != nil {
		t.uPrev.uNext = t.uNext
	} else if ch.uHead == t {
		ch.uHead = t.uNext
	}
	if t.uNext != nil {
		t.uNext.uPrev = t.uPrev
	} else if ch.uTail == t {
		ch.uTail = t.uPrev
	}
	t.uPrev, t.uNext = nil, nil
}

// sortTasks 紧急在前，同类按 ID 升序（稳定插入排序风格，规模为任务总数）。
func sortTasks(ts []*Task) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && lessTask(ts[j], ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

func lessTask(a, b *Task) bool {
	if a.Kind != b.Kind {
		return a.Kind == Urgent
	}
	return a.ID < b.ID
}
