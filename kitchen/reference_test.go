package kitchen_test

import (
	"fmt"
	"sort"
	"strings"

	"ontology/kitchen"
)

// naiveModel 是一个与生产实现完全独立的逐秒模拟模型：
// 它不使用任何生产代码，按秒推进，每个空闲制作位在每一秒
// 按“已到目标时刻的预约单优先，其次接单最早的即时单”选择开工。
type naiveModel struct {
	cfg        kitchen.Config
	t          int64
	paused     bool
	press      bool
	events     []string // "enter@t" / "exit@t"
	orders     map[string]*naiveOrder
	slots      []*naiveOrder
	seqCounter int64
	log        *strings.Builder
}

type naiveOrder struct {
	id           string
	kind         kitchen.OrderKind
	duration     int64
	targetPickup int64
	targetStart  int64
	admittedAt   int64
	status       kitchen.OrderStatus
	startAt      int64
	finishAt     int64
	promise      int64
	seq          int64
}

func newNaive(cfg kitchen.Config, log *strings.Builder) *naiveModel {
	return &naiveModel{
		cfg:    cfg,
		orders: map[string]*naiveOrder{},
		slots:  make([]*naiveOrder, cfg.Parallelism),
		log:    log,
	}
}

func (m *naiveModel) note(format string, args ...any) {
	fmt.Fprintf(m.log, "    [naive] %s\n", fmt.Sprintf(format, args...))
}

// advance 把朴素模型推进到操作时刻 t（被拒绝操作也要推进墙钟与自动开工）。
func (m *naiveModel) advance(t int64) {
	m.tickTo(t)
}

// tickTo 逐秒推进到 t，并在每一秒处理“到点自动完成 + 空位开工”。
func (m *naiveModel) tickTo(t int64) {
	for m.t < t {
		m.t++
		// 已到声明完成时刻的占用自动释放（生产模型的“接续”等价于此）。
		for si, o := range m.slots {
			if o != nil && o.startAt+o.duration <= m.t {
				m.slots[si] = nil
			}
		}
		m.assignAt(m.t)
	}
}

// assignAt 给当前所有空闲制作位安排订单。
// 每个制作位的释放时刻可能不同，因此每轮在所有“可用位”里全局挑选
// “开工时刻最早”的槽-单配对（同时刻预约插队），与生产 allocate 同构。
func (m *naiveModel) assignAt(now int64) {
	for {
		bestSlot := -1
		var best *naiveOrder
		bestStart := int64(1 << 62)
		for si, cur := range m.slots {
			if cur != nil {
				continue
			}
			var pick *naiveOrder
			pickStart := int64(1 << 62)
			for _, o := range m.orders {
				if o.status != kitchen.StatusWaiting {
					continue
				}
				ready := o.admittedAt
				if o.kind == kitchen.Reservation {
					if o.targetStart > now {
						continue
					}
					ready = o.targetStart
				}
				if ready > now {
					continue
				}
				st := ready
				if st < now {
					st = now
				}
				if pick == nil || st < pickStart ||
					(st == pickStart && m.pickBefore(o, pick)) {
					pick, pickStart = o, st
				}
			}
			if pick == nil {
				continue
			}
			if bestSlot == -1 || pickStart < bestStart ||
				(pickStart == bestStart && m.pickBefore(pick, best)) {
				bestSlot, best, bestStart = si, pick, pickStart
			}
		}
		if bestSlot == -1 {
			return
		}
		best.status = kitchen.StatusCooking
		best.startAt = bestStart
		m.slots[bestSlot] = best
	}
}

// pickBefore 给出同一“空闲秒”上两笔可开工订单的先后：
// 开工时刻相同则预约单插队；预约单之间目标开工早者优先；再以接单序决胜。
// 注意：此处只比较“现在就能开工”的订单（调用方已过滤目标时刻未到者）。
func (m *naiveModel) pickBefore(a, b *naiveOrder) bool {
	if a.kind != b.kind {
		return a.kind == kitchen.Reservation
	}
	if a.kind == kitchen.Reservation && a.targetStart != b.targetStart {
		return a.targetStart < b.targetStart
	}
	return a.seq < b.seq
}

func (m *naiveModel) estimateWait(now int64) int64 {
	// 深拷贝在制与等待订单，保证模拟不污染本体状态。
	simSlots := make([]*naiveOrder, len(m.slots))
	for si, o := range m.slots {
		if o != nil {
			cp := *o
			simSlots[si] = &cp
		}
	}
	var ws []*naiveOrder
	for _, o := range m.orders {
		if o.status == kitchen.StatusWaiting {
			cp := *o
			ws = append(ws, &cp)
		}
	}
	t := now
	for {
		// 逐秒：先到点释放，再用与 assignAt 完全相同的规则给空闲位排真实等待单。
		for si, o := range simSlots {
			if o != nil && o.startAt+o.duration <= t {
				simSlots[si] = nil
			}
		}
		for si := range simSlots {
			if simSlots[si] != nil {
				continue
			}
			var blocker *naiveOrder
			blockerStart := int64(1 << 62)
			for _, o := range ws {
				ready := o.admittedAt
				if o.kind == kitchen.Reservation {
					if o.targetStart > t {
						continue
					}
					ready = o.targetStart
				}
				if ready > t {
					continue
				}
				st := ready
				if ready < t {
					st = t
				}
				if blocker == nil || st < blockerStart ||
					(st == blockerStart && m.pickBefore(o, blocker)) {
					blocker = o
					blockerStart = st
				}
			}
			if blocker == nil {
				return t - now // 本秒该位无人可排：假想即时单在 t 开工
			}
			blocker.status = kitchen.StatusCooking
			blocker.startAt = t
			simSlots[si] = blocker
			nws := ws[:0]
			for _, o := range ws {
				if o != blocker {
					nws = append(nws, o)
				}
			}
			ws = nws
		}
		t++
	}
}

type naiveAdmit struct {
	ok            bool
	promise       int64
	estimatedWait int64
	errCode       kitchen.ErrorCode
}

// pausedAt 报告在推进到 at 之后是否处于暂停（暂停状态不随时间自动解除）。
func (m *naiveModel) pausedAt(at int64) bool {
	return m.paused
}

// estimateWaitAt 在不落地推进的前提下，估算“若时钟走到 at”下一笔假想即时单等待。
// 做法：在独立副本模型上 tickTo(at) 再 estimateWait(at)，本体零副作用。
func (m *naiveModel) estimateWaitAt(at int64) int64 {
	sim := m.fork()
	sim.tickTo(at)
	return sim.estimateWait(at)
}

// fork 深拷贝一个仅用于估算的副本。
func (m *naiveModel) fork() *naiveModel {
	cp := &naiveModel{
		cfg: m.cfg, t: m.t, paused: m.paused, press: m.press,
		orders: map[string]*naiveOrder{}, slots: make([]*naiveOrder, len(m.slots)),
		log: m.log,
	}
	clones := map[*naiveOrder]*naiveOrder{}
	for id, o := range m.orders {
		c := *o
		cp.orders[id] = &c
		clones[o] = &c
	}
	for si, o := range m.slots {
		if o != nil {
			cp.slots[si] = clones[o]
		}
	}
	return cp
}

func (m *naiveModel) admit(req kitchen.OrderRequest, at int64) naiveAdmit {
	if req.ID == "" || req.Duration <= 0 ||
		(req.Kind == kitchen.Reservation && req.TargetPickup-req.Duration < 0) {
		return naiveAdmit{errCode: kitchen.ErrInvalidParam}
	}
	if at < m.t {
		return naiveAdmit{errCode: kitchen.ErrClockBackward}
	}
	if _, exists := m.orders[req.ID]; exists {
		return naiveAdmit{errCode: kitchen.ErrOrderExists}
	}
	// 暂停判定不依赖推进，但保持与生产一致的拒绝次序（存在->暂停->预约过近->爆单）。
	if m.pausedAt(at) {
		return naiveAdmit{errCode: kitchen.ErrMerchantPaused}
	}
	if req.Kind == kitchen.Reservation {
		ts := req.TargetPickup - req.Duration
		if ts < at+m.cfg.ReservationLead {
			return naiveAdmit{errCode: kitchen.ErrReservationTooSoon}
		}
		m.advance(at)
		o := m.newOrder(req)
		o.promise = req.TargetPickup
		m.assignAt(m.t)
		m.note("admit reservation %s targetStart=%d promise=%d", o.id, o.targetStart, o.promise)
		return naiveAdmit{ok: true, promise: o.promise}
	}
	// 爆单判定在副本视图上完成，拒绝时本体不推进、不改队列。
	w := m.estimateWaitAt(at)
	if w >= m.cfg.RejectThreshold {
		m.note("reject immediate %s overloaded wait=%d", req.ID, w)
		return naiveAdmit{errCode: kitchen.ErrOverloaded, estimatedWait: w}
	}
	m.advance(at)
	if !m.press && w >= m.cfg.EnterThreshold {
		m.press = true
		m.events = append(m.events, fmt.Sprintf("enter@%d", m.t))
		m.note("PRESSURE ENTER at %d wait=%d", m.t, w)
	}
	o := m.newOrder(req)
	if w >= m.cfg.EnterThreshold {
		o.promise = m.t + w + req.Duration
	} else {
		o.promise = m.t + req.Duration + m.cfg.EnterThreshold
	}
	m.assignAt(m.t)
	after := m.estimateWait(m.t)
	if m.press && after < m.cfg.ExitThreshold {
		m.press = false
		m.events = append(m.events, fmt.Sprintf("exit@%d", m.t))
		m.note("PRESSURE EXIT at %d afterWait=%d", m.t, after)
	}
	m.note("admit immediate %s wait=%d promise=%d", o.id, w, o.promise)
	return naiveAdmit{ok: true, promise: o.promise, estimatedWait: w}
}

func (m *naiveModel) newOrder(req kitchen.OrderRequest) *naiveOrder {
	o := &naiveOrder{
		id:           req.ID,
		kind:         req.Kind,
		duration:     req.Duration,
		targetPickup: req.TargetPickup,
		admittedAt:   m.t,
		status:       kitchen.StatusWaiting,
	}
	if req.Kind == kitchen.Reservation {
		o.targetStart = req.TargetPickup - req.Duration
	}
	m.seqCounter++
	o.seq = m.seqCounter
	m.orders[req.ID] = o
	return o
}

// classifyOrderAt 在副本上推进到 at 后返回订单状态（用于无副作用的拒绝判定）。
func (m *naiveModel) classifyOrderAt(id string, at int64) (kitchen.OrderStatus, bool, int64) {
	sim := m.fork()
	sim.tickTo(at)
	o, ok := sim.orders[id]
	if !ok {
		return 0, false, 0
	}
	return o.status, true, o.startAt
}

func (m *naiveModel) complete(id string, at int64) kitchen.ErrorCode {
	if at < m.t {
		return kitchen.ErrClockBackward
	}
	if _, ok := m.orders[id]; !ok {
		return kitchen.ErrOrderNotFound
	}
	// 无副作用地判定推进后的状态。
	st, _, startAt := m.classifyOrderAt(id, at)
	if st == kitchen.StatusWaiting {
		return kitchen.ErrNotStarted
	}
	if st == kitchen.StatusCancelled {
		return kitchen.ErrNotStarted
	}
	if st == kitchen.StatusDone {
		return kitchen.ErrAlreadyDone
	}
	if at < startAt {
		return kitchen.ErrInvalidParam
	}
	m.tickTo(at)
	o := m.orders[id]
	for si, so := range m.slots {
		if so == o {
			m.slots[si] = nil
			break
		}
	}
	o.status = kitchen.StatusDone
	o.finishAt = at
	m.assignAt(at)
	if m.press && m.estimateWait(at) < m.cfg.ExitThreshold {
		m.press = false
		m.events = append(m.events, fmt.Sprintf("exit@%d", at))
		m.note("PRESSURE EXIT at %d after completion", at)
	}
	return 0
}

func (m *naiveModel) cancel(id string, at int64) kitchen.ErrorCode {
	if at < m.t {
		return kitchen.ErrClockBackward
	}
	o, ok := m.orders[id]
	if !ok {
		return kitchen.ErrOrderNotFound
	}
	st, _, _ := m.classifyOrderAt(id, at)
	if st == kitchen.StatusCooking {
		return kitchen.ErrAlreadyStarted
	}
	if st == kitchen.StatusDone {
		return kitchen.ErrAlreadyDone
	}
	if st == kitchen.StatusCancelled {
		return kitchen.ErrAlreadyDone
	}
	m.tickTo(at)
	o.status = kitchen.StatusCancelled
	if m.press && m.estimateWait(at) < m.cfg.ExitThreshold {
		m.press = false
		m.events = append(m.events, fmt.Sprintf("exit@%d", at))
		m.note("PRESSURE EXIT at %d after cancel", at)
	}
	return 0
}

func (m *naiveModel) pause(at int64) kitchen.ErrorCode {
	if at < m.t {
		return kitchen.ErrClockBackward
	}
	m.tickTo(at)
	m.paused = true
	return 0
}

func (m *naiveModel) resume(at int64) kitchen.ErrorCode {
	if at < m.t {
		return kitchen.ErrClockBackward
	}
	m.tickTo(at)
	wasPaused := m.paused
	m.paused = false
	if wasPaused && m.press && m.estimateWait(at) < m.cfg.ExitThreshold {
		m.press = false
		m.events = append(m.events, fmt.Sprintf("exit@%d", at))
		m.note("PRESSURE EXIT at %d after resume", at)
	}
	return 0
}

// orderSnapshot 是两模型对比用的归一化订单视图。
type orderSnapshot struct {
	id      string
	status  kitchen.OrderStatus
	startAt int64
	promise int64
}

func (m *naiveModel) snapshot() []orderSnapshot {
	out := make([]orderSnapshot, 0, len(m.orders))
	for _, o := range m.orders {
		out = append(out, orderSnapshot{o.id, o.status, o.startAt, o.promise})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}
