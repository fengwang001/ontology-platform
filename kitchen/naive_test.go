package kitchen

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
)

// naiveKitchen 是逐秒模拟的朴素对照模型，与被测实现独立编写：
// 每个被接受的操作先把时间逐秒推进到操作时刻（途中每一秒都检查是否有
// 订单可以开工），再应用操作本身；开工推定用逐秒暴力模拟而非堆。
type naiveKitchen struct {
	cfg        Config
	clock      int64
	clockSet   bool
	paused     bool
	seq        int64
	settled    int64
	settledSet bool
	slots      []naiveSlot
	orders     map[string]*naiveOrder
	queue      []*naiveOrder
	resv       []*naiveOrder
	pressured  bool
	events     []PressureEvent
}

type naiveSlot struct {
	busy   bool
	estEnd int64
	freeAt int64
}

type naiveOrder struct {
	id           string
	duration     int64
	reservation  bool
	acceptTime   int64
	targetStart  int64
	targetPickup int64
	seq          int64
	state        orderState
	slot         int
	startTime    int64
	promised     int64
	pressured    bool
}

func newNaiveKitchen(cfg Config) *naiveKitchen {
	return &naiveKitchen{
		cfg:    cfg,
		slots:  make([]naiveSlot, cfg.ParallelLimit),
		orders: make(map[string]*naiveOrder),
	}
}

func (n *naiveKitchen) setPressure(p bool, now int64) {
	if n.pressured != p {
		n.pressured = p
		n.events = append(n.events, PressureEvent{At: now, Enter: p})
	}
}

// advance 把时间逐秒推进到 now 之前（不含 now），途中每一秒都处理到期的
// 开工；now 时刻到期的开工由操作变更之后的 startDue(now) 处理，
// 与被测实现"先应用操作、再开工"的次序保持一致。
func (n *naiveKitchen) advance(now int64) {
	if !n.settledSet {
		n.settled = now
		n.settledSet = true
		return
	}
	for s := n.settled + 1; s < now; s++ {
		n.startDue(s)
	}
	n.settled = now
}

// startDue 在时刻 s 为所有空出的制作位按优先级开工：
// 已到达目标开工时刻的预约单优先，即时单按接单次序。
func (n *naiveKitchen) startDue(s int64) {
	for {
		slot := -1
		for i := range n.slots {
			if !n.slots[i].busy && n.slots[i].freeAt <= s &&
				(slot < 0 || n.slots[i].freeAt < n.slots[slot].freeAt) {
				slot = i
			}
		}
		if slot < 0 {
			return
		}
		var o *naiveOrder
		if len(n.resv) > 0 && n.resv[0].targetStart <= s {
			o = n.resv[0]
			n.resv = n.resv[1:]
		} else if len(n.queue) > 0 && n.queue[0].acceptTime <= s {
			o = n.queue[0]
			n.queue = n.queue[1:]
		} else {
			return
		}
		start := n.slots[slot].freeAt
		if o.reservation {
			if o.targetStart > start {
				start = o.targetStart
			}
		} else if o.acceptTime > start {
			start = o.acceptTime
		}
		o.state = stateCooking
		o.startTime = start
		o.slot = slot
		n.slots[slot].busy = true
		n.slots[slot].estEnd = start + o.duration
	}
}

// estimateWait 逐秒暴力推定假想即时单的预计等待。
func (n *naiveKitchen) estimateWait(now int64, dur int64) int64 {
	estFree := make([]int64, len(n.slots))
	for i, s := range n.slots {
		switch {
		case s.busy && s.estEnd > now:
			estFree[i] = s.estEnd
		case s.busy:
			estFree[i] = now
		default:
			estFree[i] = s.freeAt
		}
	}
	type item struct {
		dur, release, seq int64
		resv, phantom     bool
	}
	var items []item
	for _, o := range n.queue {
		items = append(items, item{dur: o.duration, release: o.acceptTime, seq: o.seq})
	}
	for _, o := range n.resv {
		items = append(items, item{dur: o.duration, release: o.targetStart, resv: true, seq: o.seq})
	}
	items = append(items, item{dur: dur, release: now, seq: n.seq, phantom: true})
	for s := now; ; s++ {
		for {
			slot := -1
			for i := range estFree {
				if estFree[i] <= s && (slot < 0 || estFree[i] < estFree[slot] ||
					(estFree[i] == estFree[slot] && i < slot)) {
					slot = i
				}
			}
			if slot < 0 {
				break
			}
			eff := estFree[slot]
			if eff < now {
				eff = now
			}
			best := -1
			for i := range items {
				if !items[i].resv || items[i].release > eff {
					continue
				}
				if best < 0 || items[i].release < items[best].release ||
					(items[i].release == items[best].release && items[i].seq < items[best].seq) {
					best = i
				}
			}
			if best < 0 {
				for i := range items {
					if items[i].resv {
						continue
					}
					if best < 0 || items[i].seq < items[best].seq {
						best = i
					}
				}
			}
			if best < 0 {
				minRel := int64(math.MaxInt64)
				for i := range items {
					if items[i].resv && items[i].release < minRel {
						minRel = items[i].release
					}
				}
				if minRel == int64(math.MaxInt64) {
					break
				}
				estFree[slot] = minRel // 制作位空转到最近的目标开工时刻
				continue
			}
			o := items[best]
			start := estFree[slot]
			if o.resv {
				if o.release > start {
					start = o.release
				}
			} else {
				start = eff
			}
			if o.phantom {
				return start - now
			}
			estFree[slot] = start + o.dur
			items = append(items[:best], items[best+1:]...)
		}
	}
}

func (n *naiveKitchen) checkClock(now int64) error {
	if n.clockSet && now < n.clock {
		return fmt.Errorf("%w: %d < %d", ErrClockRegression, now, n.clock)
	}
	return nil
}

func (n *naiveKitchen) acceptInstant(now int64, id string, dur int64) (AcceptResult, error) {
	if now < 0 || dur <= 0 {
		return AcceptResult{}, fmt.Errorf("%w", ErrInvalidArgument)
	}
	if err := n.checkClock(now); err != nil {
		return AcceptResult{}, err
	}
	if _, ok := n.orders[id]; ok {
		return AcceptResult{}, fmt.Errorf("%w", ErrOrderExists)
	}
	if n.paused {
		return AcceptResult{}, ErrMerchantPaused
	}
	wait := n.estimateWait(now, dur)
	if wait >= n.cfg.BurstThreshold {
		return AcceptResult{}, fmt.Errorf("%w", ErrBurst)
	}
	n.clock, n.clockSet = now, true
	o := &naiveOrder{id: id, duration: dur, acceptTime: now, seq: n.seq, state: stateWaiting}
	n.seq++
	if wait >= n.cfg.EnterThreshold {
		o.pressured = true
		o.promised = now + wait + dur
	} else {
		o.promised = now + dur + n.cfg.EnterThreshold
	}
	n.orders[id] = o
	if wait >= n.cfg.EnterThreshold {
		n.setPressure(true, now)
	} else if wait < n.cfg.ExitThreshold {
		n.setPressure(false, now)
	}
	n.advance(now)
	n.queue = append(n.queue, o) // advance 之后再入队，避免逐秒推进看到"未来"的订单
	n.startDue(now)
	return AcceptResult{Pressured: o.pressured, PromisedPickup: o.promised, EstimatedWait: wait}, nil
}

func (n *naiveKitchen) acceptReservation(now int64, id string, dur, targetPickup int64) (AcceptResult, error) {
	if now < 0 || dur <= 0 {
		return AcceptResult{}, fmt.Errorf("%w", ErrInvalidArgument)
	}
	if err := n.checkClock(now); err != nil {
		return AcceptResult{}, err
	}
	if _, ok := n.orders[id]; ok {
		return AcceptResult{}, fmt.Errorf("%w", ErrOrderExists)
	}
	if n.paused {
		return AcceptResult{}, ErrMerchantPaused
	}
	targetStart := targetPickup - dur
	if targetStart < now+n.cfg.ReservationLead {
		return AcceptResult{}, fmt.Errorf("%w", ErrReservationTooSoon)
	}
	n.clock, n.clockSet = now, true
	o := &naiveOrder{
		id: id, duration: dur, reservation: true, acceptTime: now,
		targetStart: targetStart, targetPickup: targetPickup,
		seq: n.seq, state: stateWaiting, promised: targetPickup,
	}
	n.seq++
	n.orders[id] = o
	idx := sort.Search(len(n.resv), func(i int) bool {
		r := n.resv[i]
		return r.targetStart > o.targetStart ||
			(r.targetStart == o.targetStart && r.seq > o.seq)
	})
	n.resv = append(n.resv, nil)
	copy(n.resv[idx+1:], n.resv[idx:])
	n.resv[idx] = o
	n.advance(now)
	n.startDue(now)
	return AcceptResult{PromisedPickup: targetPickup, EstimatedWait: targetStart - now}, nil
}

func (n *naiveKitchen) complete(now int64, id string) error {
	if now < 0 {
		return fmt.Errorf("%w", ErrInvalidArgument)
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok || o.state == stateCancelled {
		return fmt.Errorf("%w", ErrOrderNotFound)
	}
	switch o.state {
	case stateWaiting:
		return fmt.Errorf("%w", ErrNotStarted)
	case stateCompleted:
		return fmt.Errorf("%w", ErrAlreadyCompleted)
	}
	if now < o.startTime {
		return fmt.Errorf("%w", ErrInvalidArgument)
	}
	n.clock, n.clockSet = now, true
	o.state = stateCompleted
	n.slots[o.slot].busy = false
	n.slots[o.slot].freeAt = now
	n.advance(now)
	n.startDue(now)
	if wait := n.estimateWait(now, 1); wait < n.cfg.ExitThreshold {
		n.setPressure(false, now)
	}
	return nil
}

func (n *naiveKitchen) cancel(now int64, id string) error {
	if now < 0 {
		return fmt.Errorf("%w", ErrInvalidArgument)
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok || o.state == stateCancelled {
		return fmt.Errorf("%w", ErrOrderNotFound)
	}
	switch o.state {
	case stateCompleted:
		return fmt.Errorf("%w", ErrAlreadyCompleted)
	case stateCooking:
		return fmt.Errorf("%w", ErrAlreadyStarted)
	}
	n.clock, n.clockSet = now, true
	o.state = stateCancelled
	if o.reservation {
		n.resv = removeNaive(n.resv, o)
	} else {
		n.queue = removeNaive(n.queue, o)
	}
	n.advance(now)
	n.startDue(now)
	if wait := n.estimateWait(now, 1); wait < n.cfg.ExitThreshold {
		n.setPressure(false, now)
	}
	return nil
}

func (n *naiveKitchen) pause(now int64) error {
	if now < 0 {
		return fmt.Errorf("%w", ErrInvalidArgument)
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.clock, n.clockSet = now, true
	n.paused = true
	n.advance(now)
	n.startDue(now)
	return nil
}

func (n *naiveKitchen) resume(now int64) error {
	if now < 0 {
		return fmt.Errorf("%w", ErrInvalidArgument)
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.clock, n.clockSet = now, true
	n.paused = false
	n.advance(now)
	n.startDue(now)
	return nil
}

func removeNaive(list []*naiveOrder, o *naiveOrder) []*naiveOrder {
	for i, x := range list {
		if x == o {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "参数非法"
	case errors.Is(err, ErrClockRegression):
		return "时钟回退"
	case errors.Is(err, ErrOrderNotFound):
		return "订单不存在"
	case errors.Is(err, ErrOrderExists):
		return "订单已存在"
	case errors.Is(err, ErrNotStarted):
		return "未开工"
	case errors.Is(err, ErrAlreadyStarted):
		return "已开工"
	case errors.Is(err, ErrAlreadyCompleted):
		return "已完成"
	case errors.Is(err, ErrMerchantPaused):
		return "商家暂停"
	case errors.Is(err, ErrReservationTooSoon):
		return "预约过近"
	case errors.Is(err, ErrBurst):
		return "爆单"
	}
	return "未知错误"
}

// 随机生成的操作序列在被测实现、独立逐秒朴素模型、重放实现三方执行，
// 逐步比对输入、输出与压单事件，并打印判定依据。
func TestRandomSequenceMatchesNaive(t *testing.T) {
	scenarios := []struct {
		name       string
		cfg        Config
		seed       int64
		steps      int
		maxAdvance int64 // 单步最大时间前进量，稀疏场景让队列有机会排空
	}{
		{"稠密流量", Config{ParallelLimit: 2, EnterThreshold: 8, ExitThreshold: 3, BurstThreshold: 15, ReservationLead: 4}, 20261006, 4000, 4},
		{"稀疏流量", Config{ParallelLimit: 2, EnterThreshold: 8, ExitThreshold: 3, BurstThreshold: 15, ReservationLead: 4}, 99, 3000, 14},
		{"单制作位高提前量", Config{ParallelLimit: 1, EnterThreshold: 5, ExitThreshold: 2, BurstThreshold: 10, ReservationLead: 10}, 7, 3000, 6},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			runRandomComparison(t, sc.cfg, sc.seed, sc.steps, sc.maxAdvance)
		})
	}
}

// 多种子扫描：放大随机对照的覆盖面，捕捉只在特定序列下出现的分歧。
func TestRandomSequenceSeedSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("种子扫描耗时，-short 模式下跳过")
	}
	cfgs := []Config{
		{ParallelLimit: 2, EnterThreshold: 8, ExitThreshold: 3, BurstThreshold: 15, ReservationLead: 4},
		{ParallelLimit: 1, EnterThreshold: 5, ExitThreshold: 2, BurstThreshold: 10, ReservationLead: 10},
		{ParallelLimit: 3, EnterThreshold: 4, ExitThreshold: 1, BurstThreshold: 9, ReservationLead: 0},
	}
	for ci, cfg := range cfgs {
		for seed := int64(0); seed < 60; seed++ {
			t.Run(fmt.Sprintf("cfg%d/seed%d", ci, seed), func(t *testing.T) {
				runRandomComparison(t, cfg, 1000+seed, 800, 8)
			})
		}
	}
}

func runRandomComparison(t *testing.T, cfg Config, seed int64, steps int, maxAdvance int64) {
	rng := rand.New(rand.NewSource(seed))

	real, err := NewKitchen(cfg)
	if err != nil {
		t.Fatalf("NewKitchen: %v", err)
	}
	replay, err := NewKitchen(cfg)
	if err != nil {
		t.Fatalf("NewKitchen replay: %v", err)
	}
	naive := newNaiveKitchen(cfg)

	var ids []string
	nextID := 0
	now := int64(0)
	pickID := func() string {
		if len(ids) == 0 || rng.Intn(20) == 0 {
			return "ghost"
		}
		return ids[rng.Intn(len(ids))]
	}
	// 按状态挑选订单，让完成/取消更常命中有效目标，充分驱动状态迁移。
	pickByState := func(want orderState) string {
		var cand []string
		for id, o := range naive.orders {
			if o.state == want {
				cand = append(cand, id)
			}
		}
		sort.Strings(cand) // 与 map 迭代顺序无关，保证序列可复现
		if len(cand) == 0 || rng.Intn(4) == 0 {
			return pickID()
		}
		return cand[rng.Intn(len(cand))]
	}

	for step := 0; step < steps; step++ {
		if rng.Intn(10) == 0 {
			now -= int64(rng.Intn(3)) // 偶发时钟回退，应被拒绝
		} else {
			now += rng.Int63n(maxAdvance)
		}
		if now < 0 {
			now = 0
		}

		var desc string
		var gotRes, wantRes, replayRes AcceptResult
		var gotErr, wantErr, replayErr error
		isAccept := false

		op := rng.Intn(8)
		if naive.paused && rng.Intn(2) == 0 {
			op = 7 // 暂停期间更倾向恢复，避免长段全被拒
		}
		switch op {
		case 0, 1, 2: // 即时单
			id := fmt.Sprintf("o%d", nextID)
			nextID++
			dur := int64(1 + rng.Intn(8))
			if rng.Intn(25) == 0 {
				dur = 0 // 偶发非法参数
			}
			gotRes, gotErr = real.AcceptInstant(now, id, dur)
			wantRes, wantErr = naive.acceptInstant(now, id, dur)
			replayRes, replayErr = replay.AcceptInstant(now, id, dur)
			ids = append(ids, id)
			desc = fmt.Sprintf("AcceptInstant(%s, dur=%d)", id, dur)
			isAccept = true
		case 3: // 预约单
			id := fmt.Sprintf("r%d", nextID)
			nextID++
			dur := int64(1 + rng.Intn(6))
			target := now + int64(rng.Intn(30))
			gotRes, gotErr = real.AcceptReservation(now, id, dur, target)
			wantRes, wantErr = naive.acceptReservation(now, id, dur, target)
			replayRes, replayErr = replay.AcceptReservation(now, id, dur, target)
			ids = append(ids, id)
			desc = fmt.Sprintf("AcceptReservation(%s, dur=%d, pickup=%d)", id, dur, target)
			isAccept = true
		case 4: // 完成报告
			id := pickByState(stateCooking)
			gotErr = real.Complete(now, id)
			wantErr = naive.complete(now, id)
			replayErr = replay.Complete(now, id)
			desc = fmt.Sprintf("Complete(%s)", id)
		case 5: // 取消
			id := pickByState(stateWaiting)
			gotErr = real.Cancel(now, id)
			wantErr = naive.cancel(now, id)
			replayErr = replay.Cancel(now, id)
			desc = fmt.Sprintf("Cancel(%s)", id)
		case 6: // 暂停
			gotErr = real.Pause(now)
			wantErr = naive.pause(now)
			replayErr = replay.Pause(now)
			desc = "Pause()"
		default: // 恢复
			gotErr = real.Resume(now)
			wantErr = naive.resume(now)
			replayErr = replay.Resume(now)
			desc = "Resume()"
		}

		if errKind(gotErr) != errKind(wantErr) {
			dumpDivergence(t, real, naive)
			t.Fatalf("step=%d %s: 被测实现报 %s，朴素模型报 %s", step, desc, errKind(gotErr), errKind(wantErr))
		}
		if errKind(gotErr) != errKind(replayErr) {
			t.Fatalf("step=%d %s: 重放结果 %s 与首次 %s 不一致", step, desc, errKind(replayErr), errKind(gotErr))
		}
		if isAccept && gotErr == nil && (gotRes != wantRes || gotRes != replayRes) {
			dumpDivergence(t, real, naive)
			t.Fatalf("step=%d %s: 准入结果不一致 got=%+v naive=%+v replay=%+v",
				step, desc, gotRes, wantRes, replayRes)
		}

		realEvents := real.PressureEvents()
		if !eventsEqual(realEvents, naive.events) {
			t.Fatalf("step=%d %s: 压单事件不一致 real=%v naive=%v", step, desc, realEvents, naive.events)
		}
		if got := real.CookingCount(); got > cfg.ParallelLimit {
			t.Fatalf("step=%d %s: 制作中订单数 %d 突破上限 %d", step, desc, got, cfg.ParallelLimit)
		}

		t.Logf("step=%d now=%d %s => err=%s res=%+v | 判定依据: wait=%d 阈值[退出<%d 进入>=%d 爆单>=%d] 压单=%v 事件数=%d",
			step, now, desc, errKind(gotErr), gotRes, gotRes.EstimatedWait,
			cfg.ExitThreshold, cfg.EnterThreshold, cfg.BurstThreshold, real.Pressured(), len(realEvents))

		if step%97 == 0 { // 周期性全量比对订单快照
			compareAllOrders(t, step, real, naive, ids)
		}
	}
	compareAllOrders(t, steps, real, naive, ids)
	if !eventsEqual(real.PressureEvents(), replay.PressureEvents()) {
		t.Fatal("重放的压单事件序列与首次运行不一致")
	}
}

func eventsEqual(a, b []PressureEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// dumpDivergence 在比对失败时输出双方内部状态，便于定位分歧。
func dumpDivergence(t *testing.T, real *Kitchen, naive *naiveKitchen) {
	t.Helper()
	t.Logf("real: slotBusy=%v slotFreeAt=%v slotEstEnd=%v",
		real.slotBusy, real.slotFreeAt, real.slotEstEnd)
	for _, o := range real.queue {
		t.Logf("  real queue: %s dur=%d accept=%d", o.id, o.duration, o.acceptTime)
	}
	for _, o := range real.resv {
		t.Logf("  real resv: %s dur=%d targetStart=%d", o.id, o.duration, o.targetStart)
	}
	for id, o := range real.orders {
		if o.state == stateCooking {
			t.Logf("  real cooking: %s dur=%d start=%d slot=%d", id, o.duration, o.startTime, o.slot)
		}
	}
	t.Logf("naive: slots=%+v", naive.slots)
	for _, o := range naive.queue {
		t.Logf("  naive queue: %s dur=%d accept=%d", o.id, o.duration, o.acceptTime)
	}
	for _, o := range naive.resv {
		t.Logf("  naive resv: %s dur=%d targetStart=%d", o.id, o.duration, o.targetStart)
	}
	for id, o := range naive.orders {
		if o.state == stateCooking {
			t.Logf("  naive cooking: %s dur=%d start=%d slot=%d", id, o.duration, o.startTime, o.slot)
		}
	}
}

func compareAllOrders(t *testing.T, step int, real *Kitchen, naive *naiveKitchen, ids []string) {
	t.Helper()
	for _, id := range ids {
		got, gotOK := real.Order(id)
		o, wantOK := naive.orders[id]
		if gotOK != wantOK {
			t.Fatalf("step=%d 订单 %s 存在性不一致", step, id)
		}
		if !gotOK {
			continue
		}
		want := OrderInfo{
			State:          o.state.String(),
			StartTime:      o.startTime,
			PromisedPickup: o.promised,
			Pressured:      o.pressured,
		}
		if got != want {
			t.Fatalf("step=%d 订单 %s 快照不一致 got=%+v want=%+v", step, id, got, want)
		}
	}
}
