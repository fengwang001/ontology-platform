package kitchen

import (
	"sort"
	"sync"
)

// slot 是一个制作位的运行时状态：当前占用者与其推定完成时刻。
// occ==nil 表示空闲；freeAt 是该位“最近一次变空”的时刻（空闲锚点）。
type slot struct {
	occ    *order
	freeAt int64
}

// Kitchen 是单个商家厨房的全部可变状态。
//
// 并发模型：一把互斥锁串行化所有写操作与读快照，任意交错的结果
// 等价于某个全序串行顺序；所有决策只依赖整数时刻与单调接单序号，
// 因而相同操作序列重放得到相同的开工次序、承诺取货时刻与压单事件。
type Kitchen struct {
	mu      sync.Mutex
	cfg     Config
	clock   int64 // 已接受操作的最大时刻
	paused  bool
	orders  map[string]*order
	slots   []*slot  // 长度恒为 Parallelism；显式制作位占用
	waiting []*order // 未开工；预约单目标时刻未到也在此列，只是 ready 更晚
	// unreleased 记录已到推定完成时刻、制作位已被后续订单接续、
	// 但商家尚未提交完成报告的订单（值为其自动释放时刻）。
	unreleased map[*order]int64
	seq        int64
	press      pressure
}

// New 构造厨房并校验参数。
func New(cfg Config) (*Kitchen, error) {
	if cfg.Parallelism <= 0 {
		return nil, newError(ErrInvalidParam, "parallelism must be positive, got %d", cfg.Parallelism)
	}
	if cfg.EnterThreshold <= 0 {
		return nil, newError(ErrInvalidParam, "enter threshold must be positive, got %d", cfg.EnterThreshold)
	}
	if cfg.ExitThreshold >= cfg.EnterThreshold {
		return nil, newError(ErrInvalidParam, "exit threshold %d must be strictly less than enter threshold %d",
			cfg.ExitThreshold, cfg.EnterThreshold)
	}
	if cfg.RejectThreshold <= cfg.EnterThreshold {
		return nil, newError(ErrInvalidParam, "reject threshold %d must be strictly greater than enter threshold %d",
			cfg.RejectThreshold, cfg.EnterThreshold)
	}
	if cfg.ReservationLead < 0 {
		return nil, newError(ErrInvalidParam, "reservation lead must be non-negative, got %d", cfg.ReservationLead)
	}
	k := &Kitchen{cfg: cfg, orders: make(map[string]*order)}
	k.unreleased = make(map[*order]int64)
	for i := 0; i < cfg.Parallelism; i++ {
		// 初始空位的锚点为 0（全局时钟起点），而不是负无穷：
		// 同一时刻反复推定/取消时，等待不应因“自古空位”而被错误缩短。
		k.slots = append(k.slots, &slot{freeAt: 0})
	}
	return k, nil
}

func (k *Kitchen) checkClock(t int64) error {
	if t < 0 {
		return newError(ErrInvalidParam, "time must be non-negative, got %d", t)
	}
	if t < k.clock {
		return newError(ErrClockBackward, "operation time %d earlier than accepted max %d", t, k.clock)
	}
	return nil
}

// cookingOrders 返回当前制作中的订单快照视图。
func (k *Kitchen) cookingOrders() []*order {
	out := make([]*order, 0, len(k.slots))
	for _, s := range k.slots {
		if s.occ != nil {
			out = append(out, s.occ)
		}
	}
	return out
}

// slotFreeAt 逐个制作位给出“变空时刻”：在制位取 projected，空闲位取真实锚点。
func (k *Kitchen) slotFreeAt() []int64 {
	out := make([]int64, len(k.slots))
	for i, s := range k.slots {
		out[i] = s.freeAt
	}
	return out
}

// pump 以真实制作位状态做事件驱动的开工，直到 now。
//
// 规则与纯函数 allocate 一致：扫描所有此刻可用（空闲或已到推定完成）的制作位，
// 在“已到目标时刻的预约单”与“最早接单的即时单”之间取更早开工者，同时刻预约插队；
// 未来预约单不占位、不阻塞即时单。循环到没有任何位能在 now 之前开工为止。
func (k *Kitchen) pump(now int64) {
	if now > k.clock {
		k.clock = now
	}
	for k.pumpOnce(now) {
	}
}

// pumpOnce 扫描全部制作位并给一个此刻可开工的位安排订单，返回是否开工。
// 候选选择与 allocate 完全同构：在“最早可开工预约单”与“最早接单即时单”
// 之间取在该位上开工更早者，同时刻预约插队；未来预约单不参与。
func (k *Kitchen) pumpOnce(now int64) bool {
	type cand struct {
		si      int
		o       *order
		startAt int64
	}
	best := cand{si: -1}
	for si, s := range k.slots {
		if s.freeAt > now {
			continue // 无论是否占用，未到变空时刻就不可用
		}
		var ri, ii = -1, -1
		for i, o := range k.waiting {
			if o.kind == Reservation {
				if o.targetStart > now {
					continue
				}
				if ri == -1 || o.targetStart < k.waiting[ri].targetStart ||
					(o.targetStart == k.waiting[ri].targetStart && o.info.EnqueueSeq < k.waiting[ri].info.EnqueueSeq) {
					ri = i
				}
			} else if ii == -1 {
				ii = i
			}
		}
		pick, startAt := pickOrder(k.waiting, ri, ii, s.freeAt)
		if pick == nil || startAt > now {
			continue
		}
		if best.si == -1 || startAt < best.startAt ||
			(startAt == best.startAt &&
				(pick.kind == Reservation && best.o.kind == Immediate ||
					pick.kind == best.o.kind && pick.info.EnqueueSeq < best.o.info.EnqueueSeq)) {
			best = cand{si: si, o: pick, startAt: startAt}
		}
	}
	if best.si == -1 {
		return false
	}
	k.startOn(k.slots[best.si], best.o, best.startAt)
	return true
}

// pickOrder 在一个制作位上选择下一笔订单并给出开工时刻。
// ri/ii 为等待队列中最优预约单/即时单的下标（-1 表示无）。
func pickOrder(waiting []*order, ri, ii int, freeAt int64) (*order, int64) {
	var ro, io *order
	if ri >= 0 {
		ro = waiting[ri]
	}
	if ii >= 0 {
		io = waiting[ii]
	}
	switch {
	case ro != nil && io != nil:
		rs := max64(freeAt, ro.targetStart)
		is := max64(freeAt, io.admittedAt)
		if rs <= is {
			// 预约单在该位上开工更早，或开工同时刻时预约单插队优先。
			return ro, rs
		}
		return io, is
	case ro != nil:
		return ro, max64(freeAt, ro.targetStart)
	case io != nil:
		return io, max64(freeAt, io.admittedAt)
	}
	return nil, 0
}

func (k *Kitchen) startOn(s *slot, o *order, startAt int64) {
	if s.occ != nil {
		// 前一占用者已到推定完成时刻而被后续订单“接续”：
		// 记录自动释放时刻供商家完成报告核对，再释放占用。
		k.unreleased[s.occ] = s.freeAt
		s.occ = nil
	}
	s.occ = o
	s.freeAt = startAt + o.duration
	o.info.Status = StatusCooking
	o.info.StartAt = startAt
	o.projected = s.freeAt
	rest := k.waiting[:0]
	for _, wo := range k.waiting {
		if wo != o {
			rest = append(rest, wo)
		}
	}
	k.waiting = rest
}

// hypotheticalWait 从真实制作位快照前向推定下一笔假想即时单的预计等待。
// O((P+W) log P)，不触碰已完成订单，故不随历史增长。
func (k *Kitchen) hypotheticalWait(now int64) int64 {
	holder := &order{}
	cand := allocItem{o: holder, ready: now, seq: k.seq + 1, duration: 1}
	start := allocateSlots(k.cfg.Parallelism, k.slotFreeAt(), k.waiting, cand)[holder]
	return start - now
}

// idleFloor 给出当前空闲制作位的最早空出锚点；没有空位时返回 now 哨兵
// （此时空位锚点不影响结果，因为新单必须等某个在制订单完成）。
func (k *Kitchen) idleFloor() int64 {
	floor := k.clock // 没有任何制作位信息时的兜底
	for _, s := range k.slots {
		if s.freeAt < floor {
			floor = s.freeAt
		}
	}
	return floor
}

// Admit 处理一笔即时单或预约单的接单。
func (k *Kitchen) Admit(req OrderRequest, now int64) (AdmitResult, error) {
	if err := validateRequest(req); err != nil {
		return AdmitResult{}, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(now); err != nil {
		return AdmitResult{}, err
	}
	if _, ok := k.orders[req.ID]; ok {
		return AdmitResult{}, newError(ErrOrderExists, "order %q already exists", req.ID)
	}
	if k.paused {
		return AdmitResult{}, newError(ErrMerchantPaused, "merchant is paused at %d", now)
	}

	// 纯函数预演当前队列到 now 的视角：拒绝路径绝不落地。
	plan := k.virtualPlan(now)

	if req.Kind == Reservation {
		targetStart := req.TargetPickup - req.Duration
		if targetStart < now+k.cfg.ReservationLead {
			return AdmitResult{}, newError(ErrReservationTooSoon,
				"target start %d earlier than now %d + lead %d", targetStart, now, k.cfg.ReservationLead)
		}
		o := k.newOrder(req, now)
		o.info.PromisePickup = req.TargetPickup
		k.commitOrder(o, now, plan)
		return AdmitResult{
			Accepted:        true,
			PromisePickup:   req.TargetPickup,
			EstimatedStart:  targetStart,
			EstimatedFinish: req.TargetPickup,
			Pressed:         k.press.pressed,
		}, nil
	}

	start := k.estimateAfterPlan(plan, now)
	wait := start - now
	if wait >= k.cfg.RejectThreshold {
		return AdmitResult{}, newError(ErrOverloaded,
			"expected wait %d >= reject threshold %d for order %q", wait, k.cfg.RejectThreshold, req.ID)
	}

	k.press.observeEnter(now, wait, k.cfg)

	o := k.newOrder(req, now)
	o.info.EstimatedStart = start
	k.commitOrder(o, now, plan)

	var promise int64
	if wait >= k.cfg.EnterThreshold {
		promise = start + req.Duration
	} else {
		promise = now + req.Duration + k.cfg.EnterThreshold
	}
	o.info.PromisePickup = promise

	after := k.hypotheticalWait(now)
	k.press.observeExit(now, after, k.cfg, "after admitting immediate order")

	return AdmitResult{
		Accepted:        true,
		PromisePickup:   promise,
		EstimatedStart:  start,
		EstimatedFinish: start + req.Duration,
		ExpectedWait:    wait,
		Pressed:         k.press.pressed,
	}, nil
}

func validateRequest(req OrderRequest) error {
	if req.ID == "" {
		return newError(ErrInvalidParam, "order id must not be empty")
	}
	if req.Duration <= 0 {
		return newError(ErrInvalidParam, "duration must be positive, got %d", req.Duration)
	}
	if req.Kind != Immediate && req.Kind != Reservation {
		return newError(ErrInvalidParam, "unknown order kind %d", req.Kind)
	}
	if req.Kind == Reservation && req.TargetPickup-req.Duration < 0 {
		return newError(ErrInvalidParam, "reservation target start must not be negative")
	}
	return nil
}

func (k *Kitchen) newOrder(req OrderRequest, now int64) *order {
	k.seq++
	o := &order{
		kind:       req.Kind,
		duration:   req.Duration,
		admittedAt: now,
		info: OrderInfo{
			ID:           req.ID,
			Kind:         req.Kind,
			Duration:     req.Duration,
			TargetPickup: req.TargetPickup,
			Status:       StatusWaiting,
			EnqueueSeq:   k.seq,
		},
	}
	if req.Kind == Reservation {
		o.targetStart = req.TargetPickup - req.Duration
	}
	return o
}

// commitOrder 接受订单：先按预演计划落地既已发生的开工（真实制作位），
// 再入队新单并 pump，使空闲位/已到目标时刻的预约单立即开工。
func (k *Kitchen) commitOrder(o *order, now int64, plan map[*order]int64) {
	if now > k.clock {
		k.clock = now
	}
	// 计划中的开工按 startAt、seq 依次落到真实空闲制作位。
	type ps struct {
		o *order
		t int64
	}
	var list []ps
	for wo, t := range plan {
		list = append(list, ps{wo, t})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].t != list[j].t {
			return list[i].t < list[j].t
		}
		return list[i].o.info.EnqueueSeq < list[j].o.info.EnqueueSeq
	})
	planned := make(map[*order]bool, len(list))
	for _, e := range list {
		k.assignPlanned(e.o, e.t)
		planned[e.o] = true
	}
	rest := k.waiting[:0]
	for _, wo := range k.waiting {
		if !planned[wo] {
			rest = append(rest, wo)
		}
	}
	k.waiting = rest
	k.orders[o.info.ID] = o
	k.waiting = append(k.waiting, o)
	// 旧的等待单已按预演计划落地；新单若此刻可立即开工（空闲制作位，
	// 或预约单目标时刻已到），在此开工。pump 只会落地 startAt<=now 的订单，
	// 不会把未来才开工的排队单提前置为制作中。
	k.pump(now)
}

// assignPlanned 把预演的开工落地到一个 freeAt<=t 的空闲制作位。
func (k *Kitchen) assignPlanned(o *order, t int64) {
	si := -1
	for i, s := range k.slots {
		// 空闲位可直接用；占用位若已到推定完成时刻，也可被预演订单接续。
		if s.freeAt <= t && (si == -1 || s.freeAt < k.slots[si].freeAt) {
			si = i
		}
	}
	if si == -1 {
		// 与纯函数推定保持一致的前提是计划可行；不可达则属于内部不变量破坏。
		panic("kitchen: no free slot while materializing plan")
	}
	o.info.Status = StatusCooking
	o.info.StartAt = t
	o.projected = t + o.duration
	if old := k.slots[si].occ; old != nil {
		// 旧占用者到点自动释放，等待商家完成报告记账。
		k.unreleased[old] = k.slots[si].freeAt
	}
	k.slots[si].occ = o
	k.slots[si].freeAt = o.projected
}

// Complete 报告订单完成；允许提前完成，提前完成立即释放制作位并重排。
//
// 若订单已到推定完成时刻、制作位已被后续订单接续（商家迟到报告），
// 报告只做完成记账，不再影响排产（自动释放时刻 >= 开工时刻，参数恒合法）。
func (k *Kitchen) Complete(id string, at int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(at); err != nil {
		return err
	}
	o, ok := k.orders[id]
	if !ok {
		return newError(ErrOrderNotFound, "order %q not found", id)
	}
	switch o.info.Status {
	case StatusDone:
		return newError(ErrAlreadyDone, "order %q already done", id)
	case StatusCancelled:
		return newError(ErrNotStarted, "order %q was cancelled before start", id)
	case StatusWaiting:
		// 推进前先用纯预演判断该单此刻是否本应已开工。
		if _, willStart := k.virtualPlan(at)[o]; !willStart {
			return newError(ErrNotStarted, "order %q has not started", id)
		}
	}

	// 先推进队列（可能把本单自动释放并接续给后续订单）。
	k.pump(at)

	if _, auto := k.unreleased[o]; auto {
		// 迟到报告：制作位早已按推定完成时刻接续给后续订单。
		// 若报告时刻晚于自动释放时刻（商家延迟报告），排队推进只需到 at；
		// 压单退出仍须按“完成报告之后重新推定”评估一次。
		delete(k.unreleased, o)
		o.info.Status = StatusDone
		o.info.FinishAt = at
		k.pump(at)
		wait := k.hypotheticalWait(at)
		k.press.observeExit(at, wait, k.cfg, "after completion report")
		return nil
	}

	// 仍占用制作位：定位并释放。
	var occupied *slot
	for _, s := range k.slots {
		if s.occ == o {
			occupied = s
			break
		}
	}
	if occupied == nil {
		// 状态为 Cooking 但既不在位上也不在自动释放索引：内部不变量破坏。
		return newError(ErrInvalidParam, "order %q not occupying any slot", id)
	}
	if at < o.info.StartAt {
		return newError(ErrInvalidParam, "complete time %d earlier than start time %d", at, o.info.StartAt)
	}
	occupied.occ = nil
	occupied.freeAt = at // 提前/正常完成：锚定实际报告时刻
	o.info.Status = StatusDone
	o.info.FinishAt = at

	k.pump(at)
	wait := k.hypotheticalWait(at)
	k.press.observeExit(at, wait, k.cfg, "after completion report")
	return nil
}

// Cancel 取消一笔尚未开工的排队订单，立即释放其队列位置。
func (k *Kitchen) Cancel(id string, at int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(at); err != nil {
		return err
	}
	o, ok := k.orders[id]
	if !ok {
		return newError(ErrOrderNotFound, "order %q not found", id)
	}
	switch o.info.Status {
	case StatusCooking:
		return newError(ErrAlreadyStarted, "order %q already started", id)
	case StatusDone:
		return newError(ErrAlreadyDone, "order %q already done", id)
	case StatusCancelled:
		return newError(ErrAlreadyDone, "order %q already cancelled", id)
	}
	if _, willStart := k.virtualPlan(at)[o]; willStart {
		return newError(ErrAlreadyStarted, "order %q would already have started at %d", id, at)
	}

	// 该单此刻仍未开工：先把其他既已发生的开工落地，再摘除该单。
	k.pump(at)
	rest := k.waiting[:0]
	for _, wo := range k.waiting {
		if wo != o {
			rest = append(rest, wo)
		}
	}
	k.waiting = rest
	o.info.Status = StatusCancelled

	wait := k.hypotheticalWait(at)
	k.press.observeExit(at, wait, k.cfg, "after cancelling waiting order")
	return nil
}

// Pause 商家手动暂停；幂等，不产生也不改变压单记录。
func (k *Kitchen) Pause(at int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(at); err != nil {
		return err
	}
	k.pump(at) // 排队中与制作中订单照常推进
	k.paused = true
	return nil
}

// Resume 恢复营业；恢复后从当时队列重新推定压单退出。幂等。
func (k *Kitchen) Resume(at int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(at); err != nil {
		return err
	}
	k.pump(at)
	if k.paused {
		k.paused = false
		wait := k.hypotheticalWait(at)
		k.press.observeExit(at, wait, k.cfg, "after resume: re-estimated from current queue")
	}
	return nil
}

// Tick 是纯心跳：只推进时钟并落地已到时刻的开工，不做压单判定。
func (k *Kitchen) Tick(at int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(at); err != nil {
		return err
	}
	k.pump(at)
	return nil
}

// Now 返回已接受操作的最大时刻。
func (k *Kitchen) Now() int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.clock
}

// Paused 返回商家是否处于手动暂停。
func (k *Kitchen) Paused() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.paused
}

// Pressed 返回当前是否处于压单状态。
func (k *Kitchen) Pressed() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.press.pressed
}

// PressureEvents 返回压单事件记录的副本。
func (k *Kitchen) PressureEvents() []PressureEvent {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.press.snapshotEvents()
}

// Order 返回单笔订单的只读快照；不存在时 ok 为 false。
func (k *Kitchen) Order(id string) (OrderInfo, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	o, ok := k.orders[id]
	if !ok {
		return OrderInfo{}, false
	}
	return o.info, true
}

// Counters 返回当前在制数与等待数。
func (k *Kitchen) Counters() (cooking, waiting int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.cookingOrders()), len(k.waiting)
}

// CookingOrders 返回当前制作中订单的快照，按开工时刻排序。
func (k *Kitchen) CookingOrders() []OrderInfo {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]OrderInfo, 0, len(k.slots))
	for _, s := range k.slots {
		if s.occ != nil {
			out = append(out, s.occ.info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartAt < out[j].StartAt })
	return out
}

// --- 纯函数虚拟视图：供拒绝路径在不落地的情况下读取“推进到 t 后”的状态 ---

// virtualPlan 预演到 t 为止本应已开工的等待单及其开工时刻。
// 输入为真实制作位占用（projected=推定完成）与等待队列。
func (k *Kitchen) virtualPlan(t int64) map[*order]int64 {
	starts := allocateSlots(k.cfg.Parallelism, k.slotFreeAt(), k.waiting)
	m := make(map[*order]int64, len(k.waiting))
	for _, o := range k.waiting {
		if s, ok := starts[o]; ok && s <= t {
			m[o] = s
		}
	}
	return m
}

// virtualWaiting 返回应用预演计划后仍在等待的订单。
func (k *Kitchen) virtualWaiting(plan map[*order]int64) []*order {
	out := make([]*order, 0, len(k.waiting))
	for _, o := range k.waiting {
		if _, started := plan[o]; !started {
			out = append(out, o)
		}
	}
	return out
}

// estimateAfterPlan 在“预演到 now 后”的制作位与队列上，推定一笔此刻接单的
// 假想即时单的开工时刻。预演中新开工订单占用真实位并把该位 freeAt 顺延。
func (k *Kitchen) estimateAfterPlan(plan map[*order]int64, now int64) int64 {
	slotFree := k.slotFreeAt()
	type pe struct {
		o *order
		t int64
	}
	var list []pe
	for o, t := range plan {
		list = append(list, pe{o, t})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].t != list[j].t {
			return list[i].t < list[j].t
		}
		return list[i].o.info.EnqueueSeq < list[j].o.info.EnqueueSeq
	})
	for _, e := range list {
		bi := 0
		for i := 1; i < len(slotFree); i++ {
			if slotFree[i] < slotFree[bi] {
				bi = i
			}
		}
		start := max64(slotFree[bi], e.t)
		slotFree[bi] = start + e.o.duration
	}
	holder := &order{}
	cand := allocItem{o: holder, ready: now, seq: k.seq + 1, duration: 1}
	return allocateSlots(k.cfg.Parallelism, slotFree, k.virtualWaiting(plan), cand)[holder]
}
