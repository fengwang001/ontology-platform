package preauth

import "container/heap"

// expiryEntry 记录某个授权当前持有的到期日与金额。
// 每个授权在堆中至多有一条“当前”条目；增量/捕获改期或改额时，旧条目
// 标记为 dead 并推入新条目（惰性删除）。
type expiryEntry struct {
	authID string
	day    int64
	amount int64
	dead   bool
}

type expiryHeap []*expiryEntry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].day < h[j].day }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *expiryHeap) Push(x any) { *h = append(*h, x.(*expiryEntry)) }

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// expirySchedule 是单账户的到期表，仅在所属账户锁下访问。
//
// 持久不变量（foldPersist 之后）：
//   - horizon 只增不减，是已折叠到的日期水位；
//   - 所有 day < horizon 的活条目均已出堆并从 holdTotal 扣除；
//   - 每个仍有效授权的持有额等于其当前活条目金额。
type expirySchedule struct {
	entries   expiryHeap
	horizon   int64
	holdTotal int64
}

func newExpirySchedule() *expirySchedule {
	s := &expirySchedule{}
	heap.Init(&s.entries)
	return s
}

// place 登记一笔持有并返回堆条目。
func (s *expirySchedule) place(authID string, day, amount int64) *expiryEntry {
	e := &expiryEntry{authID: authID, day: day, amount: amount}
	heap.Push(&s.entries, e)
	s.holdTotal += amount
	return e
}

// replace 迁移旧条目到新到期日、新金额（增量授权：增额并重置有效期）。
func (s *expirySchedule) replace(old *expiryEntry, day, amount int64) *expiryEntry {
	if old != nil {
		old.dead = true
		s.holdTotal -= old.amount
	}
	e := &expiryEntry{authID: old.authID, day: day, amount: amount}
	heap.Push(&s.entries, e)
	s.holdTotal += amount
	return e
}

// release 作废整条（撤销、终捕），全额移出持有合计。
func (s *expirySchedule) release(e *expiryEntry) {
	if e == nil || e.dead {
		return
	}
	e.dead = true
	s.holdTotal -= e.amount
}

// reduce 将持有减少 amount（捕获把持有转入账）。
func (s *expirySchedule) reduce(e *expiryEntry, amount int64) {
	if e == nil || e.dead || amount <= 0 {
		return
	}
	deduct := amount
	if deduct > e.amount {
		deduct = e.amount // 剩余持有不为负；超出部分由可用额度（posted）承担
	}
	e.amount -= deduct
	s.holdTotal -= deduct
}

// peekHolds 非破坏性读取 now 时刻的持有合计：临时取出到期前缀，读数后
// 立刻把条目压回并恢复 holdTotal，水位与堆内容最终完全不变。
//
// 查询绝不折叠、绝不推进水位；账户锁保证临时操作期间无并发。
func (s *expirySchedule) peekHolds(now int64) int64 {
	snap := s.transientFold(now)
	v := s.holdTotal
	s.undoFold(snap)
	return v
}

// holds 返回当前持久水位处的持有合计；操作路径在 foldPersist 之后使用。
func (s *expirySchedule) holds() int64 { return s.holdTotal }

// transientFold 临时折叠到 now（若 now 不超过水位则返回 nil），返回用于
// 撤销的快照。适用于“需要按 now 时刻持有做校验，但校验可能拒绝”的场景：
// 拒绝时调用 undoFold 即可零副作用恢复。
type foldSnapshot struct {
	entries []*expiryEntry
	amounts []int64
	deads   []bool
	horizon int64
}

func (s *expirySchedule) transientFold(now int64) *foldSnapshot {
	if now <= s.horizon {
		return nil
	}
	snap := &foldSnapshot{horizon: s.horizon}
	for s.entries.Len() > 0 {
		top := s.entries[0]
		if top.day >= now {
			break
		}
		heap.Pop(&s.entries)
		var amt int64
		if !top.dead {
			amt = top.amount
			s.holdTotal -= top.amount
			top.amount = 0
		}
		snap.entries = append(snap.entries, top)
		snap.amounts = append(snap.amounts, amt)
		snap.deads = append(snap.deads, top.dead)
	}
	s.horizon = now
	return snap
}

func (s *expirySchedule) undoFold(snap *foldSnapshot) {
	if snap == nil {
		return
	}
	for i := len(snap.entries) - 1; i >= 0; i-- {
		e := snap.entries[i]
		if !snap.deads[i] {
			e.amount = snap.amounts[i]
		}
		heap.Push(&s.entries, e)
		s.holdTotal += snap.amounts[i]
	}
	s.horizon = snap.horizon
}
