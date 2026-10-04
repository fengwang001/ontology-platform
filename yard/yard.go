package yard

import (
	"container/heap"
	"sync"

	"ontology/appt"
	"ontology/dock"
)

type Kind = appt.Kind

const (
	Dry    = appt.Dry
	Reefer = appt.Reefer
)

type Assignment struct {
	Truck []byte
	Dock  []byte
}

type status int

const (
	stWaiting status = iota
	stServing
	stDeparted
)

type waiter struct {
	truck  []byte
	kind   Kind
	seq    int64
	ciTime int64
	s      int64 // 准点车的预约起点；候补为 0
	ontime bool
}

type Yard struct {
	cfg   appt.Config
	appts *appt.Bookings
	docks *dock.Docks

	mu      sync.Mutex
	lastNow int64
	nextSeq int64

	state   map[string]status
	serving map[string][]byte // truck -> dock
	dockOf  map[string]string // dock -> truck（占用中）

	// 每个类别 × 类型一个支持 O(log n) 真正删除的最小堆。
	standby [2]*idxHeap // 键：签到序号
	ontime  [2]*idxHeap // 键：(预约起点 s, 签到序号)
	rec     map[string]*heapItem

	// looked 为一次 dispatch 中考察过的等待车辆记录数（非导出，供测试证明复杂度）。
	looked int
}

func New(cfg appt.Config) *Yard {
	y := &Yard{
		cfg:     cfg,
		appts:   appt.New(cfg),
		docks:   dock.New(cfg),
		state:   make(map[string]status),
		serving: make(map[string][]byte),
		dockOf:  make(map[string]string),
		rec:     make(map[string]*heapItem),
	}
	for k := range y.standby {
		y.standby[k] = &idxHeap{less: lessBySeq}
		y.ontime[k] = &idxHeap{less: lessBySSeq}
	}
	return y
}

func (y *Yard) Book(truck []byte, kind Kind, s, now int64) error {
	if !appt.ValidID(truck) || (kind != Dry && kind != Reefer) || s < 0 ||
		s%y.cfg.S != 0 || s < now {
		return appt.ErrInvalid
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	if now < y.lastNow {
		return appt.ErrClock
	}
	if err := y.appts.Book(truck, kind, s, now); err != nil {
		return err
	}
	y.lastNow = now
	return nil
}

func (y *Yard) AddDock(id []byte, kind Kind, now int64) ([]Assignment, error) {
	if !appt.ValidID(id) || (kind != Dry && kind != Reefer) || now < 0 {
		return nil, appt.ErrInvalid
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	if now < y.lastNow {
		return nil, appt.ErrClock
	}
	if err := y.docks.AddDock(id, kind, now); err != nil {
		return nil, err
	}
	y.lastNow = now
	return y.dispatch(now), nil
}

func (y *Yard) CheckIn(truck []byte, kind Kind, now int64) ([]Assignment, error) {
	if !appt.ValidID(truck) || (kind != Dry && kind != Reefer) || now < 0 {
		return nil, appt.ErrInvalid
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	if now < y.lastNow {
		return nil, appt.ErrClock
	}
	key := string(truck)
	if _, seen := y.state[key]; seen {
		// 已签到（等待/在月台/已离场）不可再次签到。
		return nil, appt.ErrState
	}
	ontime := false
	var s int64
	if slot, ok := y.appts.Lookup(truck); ok {
		if slot.Kind != kind {
			return nil, appt.ErrState
		}
		if now >= slot.S-y.cfg.E && now <= slot.S+y.cfg.L {
			ontime = true
			s = slot.S
			y.appts.Consume(truck)
		} else {
			y.appts.Void(truck) // 早到/迟到降候补，预约作废
		}
	}
	y.nextSeq++
	w := &waiter{
		truck: cloneBytes(truck), kind: kind, seq: y.nextSeq,
		ciTime: now, s: s, ontime: ontime,
	}
	y.state[key] = stWaiting
	y.pushWaiter(w)
	y.lastNow = now
	return y.dispatch(now), nil
}

func (y *Yard) Depart(truck []byte, now int64) ([]Assignment, error) {
	if !appt.ValidID(truck) || now < 0 {
		return nil, appt.ErrInvalid
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	if now < y.lastNow {
		return nil, appt.ErrClock
	}
	key := string(truck)
	st, seen := y.state[key]
	if !seen {
		return nil, appt.ErrNotFound
	}
	if st != stServing {
		return nil, appt.ErrState
	}
	did := y.serving[key]
	delete(y.serving, key)
	delete(y.dockOf, string(did))
	y.state[key] = stDeparted
	y.docks.Release(did)
	y.lastNow = now
	return y.dispatch(now), nil
}

// dispatch 反复「选一步」直到无车可派。每步从优先序头部重选。
func (y *Yard) dispatch(now int64) []Assignment {
	y.looked = 0
	var out []Assignment
	for {
		w, borrow := y.pick(now)
		if w == nil {
			return out
		}
		did, ok := y.docks.Take(w.kind, borrow)
		if !ok {
			// 理论不可达：pick 已确认可派。保守停止以免忙循环。
			return out
		}
		y.popWaiter(w)
		key := string(w.truck)
		y.state[key] = stServing
		y.serving[key] = did
		y.dockOf[string(did)] = key
		out = append(out, Assignment{Truck: cloneBytes(w.truck), Dock: did})
	}
}

// pick 选出优先序中第一辆此刻可派的车。
// 每堆只检查堆头（至多 6 条记录），与等待总车数无关。
func (y *Yard) pick(now int64) (*waiter, bool) {
	waitingReefer := y.standby[Reefer].Len()+y.ontime[Reefer].Len() > 0
	freeDry := y.docks.FreeDry()
	freeReefer := y.docks.FreeReefer()

	var best *waiter
	bestBorrow := false

	consider := func(w *waiter, tier int, borrow bool) {
		if best != nil {
			br := bestTier(best, now, y.cfg.Wmax)
			nr := tier
			if nr > br {
				return
			}
			if nr == br {
				if w.ontime && best.ontime {
					if w.s > best.s || (w.s == best.s && w.seq > best.seq) {
						return
					}
				} else if w.seq > best.seq {
					return
				}
			}
		}
		best = w
		bestBorrow = borrow
	}

	feasible := func(k Kind) (bool, bool) {
		if k == Reefer {
			return freeReefer, false
		}
		if freeDry {
			return true, false
		}
		if freeReefer && !waitingReefer {
			return true, true
		}
		return false, false
	}

	var standbyHead [2]*waiter
	for k := Dry; k <= Reefer; k++ {
		if w := y.standby[k].peek(); w != nil {
			standbyHead[k] = w
			y.looked++ // 每个候补堆头每步只考察这一次
		}
	}

	// 第一档：已提升候补（恰等 Wmax 即提升），按签到序号。
	for k := Dry; k <= Reefer; k++ {
		w := standbyHead[k]
		if w == nil || now-w.ciTime < y.cfg.Wmax {
			continue
		}
		if ok, borrow := feasible(k); ok {
			consider(w, 0, borrow)
		}
	}
	// 第二档：准点车 (s, 序号)。
	for k := Dry; k <= Reefer; k++ {
		w := y.ontime[k].peek()
		if w == nil {
			continue
		}
		y.looked++
		if ok, borrow := feasible(k); ok {
			consider(w, 1, borrow)
		}
	}
	// 第三档：未提升候补，按签到序号。
	for k := Dry; k <= Reefer; k++ {
		w := standbyHead[k]
		if w == nil {
			continue
		}
		if now-w.ciTime >= y.cfg.Wmax {
			continue // 已在第一档考察过
		}
		if ok, borrow := feasible(k); ok {
			consider(w, 2, borrow)
		}
	}
	return best, bestBorrow
}

func bestTier(w *waiter, now, wmax int64) int {
	if !w.ontime && now-w.ciTime >= wmax {
		return 0
	}
	if w.ontime {
		return 1
	}
	return 2
}

func cloneBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// ---------- 带索引删除的最小堆 ----------

type heapItem struct {
	w    *waiter
	idx  int
	dead bool
}

type idxHeap struct {
	items []*heapItem
	less  func(a, b *waiter) bool
}

func lessBySeq(a, b *waiter) bool { return a.seq < b.seq }
func lessBySSeq(a, b *waiter) bool {
	if a.s != b.s {
		return a.s < b.s
	}
	return a.seq < b.seq
}

func (h *idxHeap) Len() int { return len(h.items) }
func (h *idxHeap) Less(i, j int) bool {
	return h.less(h.items[i].w, h.items[j].w)
}
func (h *idxHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].idx = i
	h.items[j].idx = j
}
func (h *idxHeap) Push(x any) {
	it := x.(*heapItem)
	it.idx = len(h.items)
	h.items = append(h.items, it)
}
func (h *idxHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

func (h *idxHeap) peek() *waiter {
	if len(h.items) == 0 {
		return nil
	}
	return h.items[0].w
}

func (y *Yard) pushWaiter(w *waiter) {
	it := &heapItem{w: w}
	h := y.targetHeap(w)
	heap.Push(h, it)
	y.rec[string(w.truck)] = it
}

func (y *Yard) popWaiter(w *waiter) {
	it := y.rec[string(w.truck)]
	h := y.targetHeap(w)
	heap.Remove(h, it.idx)
	delete(y.rec, string(w.truck))
}

func (y *Yard) targetHeap(w *waiter) *idxHeap {
	if w.ontime {
		return y.ontime[w.kind]
	}
	return y.standby[w.kind]
}
