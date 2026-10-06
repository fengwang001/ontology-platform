package fulfillment

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// 朴素对照模型：不做任何增量维护，每次需要时基于全量历史逐笔重算。
// 与 System 的增量实现完全独立，用于随机操作序列的差分对比。

type nWeather struct {
	start, end int64
	ext        int64
	seq        int // 被接受时的序号
}

type nOrder struct {
	params       Params
	stage        Stage
	acceptTs     int64
	dispatchTs   int64
	mealReadyTs  int64
	pickupTs     int64
	deliverTs    int64
	deliverSeq   int
	cancelled    bool
	cancelSeq    int
	addrChanged  bool
	addrChangeTs int64
	settled      bool
	verdict      Verdict
}

type naive struct {
	params     Params
	clock      int64
	hasClock   bool
	seq        int
	orders     map[string]*nOrder
	weathers   []nWeather
	weatherIDs map[string]bool
	ledger     []Verdict
}

func newNaive(p Params) *naive {
	return &naive{params: p, orders: map[string]*nOrder{}, weatherIDs: map[string]bool{}}
}

func (n *naive) checkClock(ts int64) error {
	if n.hasClock && ts < n.clock {
		return ErrClockRollback
	}
	return nil
}

func (n *naive) advance(ts int64) {
	n.clock, n.hasClock = ts, true
	n.seq++
}

// extensionOf 全量重算订单延展：扫描所有已登记天气事件，
// 送达/取消之后登记的事件不追溯，最后按上限整体截断。
func (n *naive) extensionOf(o *nOrder) int64 {
	cutoff := n.seq // 未送达未取消：当前已登记事件全部适用
	if o.stage == StageDelivered && o.deliverSeq < cutoff {
		cutoff = o.deliverSeq
	}
	if o.cancelled && o.cancelSeq < cutoff {
		cutoff = o.cancelSeq
	}
	ext := int64(0)
	if o.addrChanged {
		ext += o.params.AddressExtension
	}
	origPromise := o.acceptTs + o.params.PromiseDuration
	for _, w := range n.weathers {
		if w.seq >= cutoff {
			continue
		}
		if w.start <= origPromise && origPromise < w.end {
			ext += w.ext
		}
	}
	if ext > o.params.ExtensionCap {
		ext = o.params.ExtensionCap
	}
	return ext
}

func (n *naive) promiseOf(o *nOrder) int64 {
	return o.acceptTs + o.params.PromiseDuration + n.extensionOf(o)
}

func (n *naive) accept(id string, ts int64) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := n.checkClock(ts); err != nil {
		return err
	}
	if _, ok := n.orders[id]; ok {
		return ErrOrderExists
	}
	n.orders[id] = &nOrder{params: n.params.clone(), stage: StageAccepted, acceptTs: ts}
	n.advance(ts)
	return nil
}

func (n *naive) event(id string, ts int64, want Stage, set func(o *nOrder)) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := n.checkClock(ts); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	if o.cancelled {
		return ErrOrderCancelled
	}
	if o.stage != want {
		return ErrEventOrder
	}
	set(o)
	n.advance(ts)
	return nil
}

func (n *naive) cancel(id string, ts int64) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := n.checkClock(ts); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	if o.cancelled {
		return ErrOrderCancelled
	}
	o.cancelled, o.cancelSeq = true, n.seq
	n.advance(ts)
	return nil
}

func (n *naive) changeAddress(id string, ts int64) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := n.checkClock(ts); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	if o.cancelled {
		return ErrOrderCancelled
	}
	if o.stage == StageDelivered {
		return ErrEventOrder
	}
	if o.addrChanged {
		return ErrAddrChanged
	}
	o.addrChanged, o.addrChangeTs = true, ts
	n.advance(ts)
	return nil
}

func (n *naive) registerWeather(id string, start, end, ext, ts int64) error {
	if id == "" || start >= end || ext < 0 {
		return ErrInvalidParam
	}
	if err := n.checkClock(ts); err != nil {
		return err
	}
	if n.weatherIDs[id] {
		return ErrInvalidParam
	}
	n.weatherIDs[id] = true
	n.weathers = append(n.weathers, nWeather{start: start, end: end, ext: ext, seq: n.seq})
	n.advance(ts)
	return nil
}

func (n *naive) updateParams(p Params, ts int64) error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := n.checkClock(ts); err != nil {
		return err
	}
	n.params = p
	n.advance(ts)
	return nil
}

// adjudicate 朴素裁决：承诺、延误、档位、归因全部当场重算。
func (n *naive) adjudicate(o *nOrder, id string, auto bool) (Verdict, string) {
	promise := n.promiseOf(o)
	delay := o.deliverTs - promise
	tier, amount := -1, int64(0)
	for i, tr := range o.params.Tiers {
		if delay >= tr.Threshold {
			tier, amount = i, tr.Amount
		}
	}
	merchant := max(0, o.mealReadyTs-(o.acceptTs+o.params.PrepAllowance))
	platform := max(0, o.dispatchTs-(o.acceptTs+o.params.DispatchAllowance))
	rider := max(0, o.pickupTs-(o.mealReadyTs+o.params.PickupAllowance))
	remaining := max(0, promise-o.pickupTs)
	rider += max(0, o.deliverTs-(o.pickupTs+remaining))
	user := int64(0)
	if o.addrChanged && o.addrChangeTs > o.pickupTs {
		user = o.params.AddressExtension
	}
	resp, best := Merchant, merchant
	if rider > best {
		resp, best = Rider, rider
	}
	if platform > best {
		resp, best = Platform, platform
	}
	if user > best {
		resp, best = User, user
	}
	if best == 0 {
		resp = Platform
	}
	if resp == User {
		amount = 0
	}
	v := Verdict{OrderID: id, Promise: promise, DeliverTs: o.deliverTs, Delay: delay,
		Tier: tier, Amount: amount, Responsible: resp, Auto: auto}
	o.settled, o.verdict = true, v
	reason := fmt.Sprintf("promise=%d delay=%d tier=%d attr=[m=%d r=%d p=%d u=%d] resp=%v amount=%d",
		promise, delay, tier, merchant, rider, platform, user, resp, amount)
	return v, reason
}

func (n *naive) claim(id string, ts int64) (Verdict, error, string) {
	if id == "" {
		return Verdict{}, ErrInvalidParam, "empty id"
	}
	if err := n.checkClock(ts); err != nil {
		return Verdict{}, err, "clock rollback"
	}
	o, ok := n.orders[id]
	if !ok {
		return Verdict{}, ErrOrderNotFound, "no such order"
	}
	if o.cancelled {
		return Verdict{}, ErrOrderCancelled, "cancelled"
	}
	if o.settled {
		return Verdict{}, ErrAlreadyPaid, "already settled"
	}
	if o.stage != StageDelivered {
		return Verdict{}, ErrNotDelivered, fmt.Sprintf("stage=%v", o.stage)
	}
	if ts >= o.deliverTs+o.params.ClaimWindow {
		return Verdict{}, ErrWindowExpired, fmt.Sprintf("ts=%d >= deliver=%d + window=%d", ts, o.deliverTs, o.params.ClaimWindow)
	}
	if o.deliverTs-n.promiseOf(o) <= 0 {
		return Verdict{}, ErrNoDelay, "delay <= 0"
	}
	v, reason := n.adjudicate(o, id, false)
	n.ledger = append(n.ledger, v)
	n.advance(ts)
	return v, nil, reason
}

func (n *naive) autoSettle(id string, ts int64) (Verdict, error, string) {
	if id == "" {
		return Verdict{}, ErrInvalidParam, "empty id"
	}
	if err := n.checkClock(ts); err != nil {
		return Verdict{}, err, "clock rollback"
	}
	o, ok := n.orders[id]
	if !ok {
		return Verdict{}, ErrOrderNotFound, "no such order"
	}
	if o.cancelled {
		return Verdict{}, ErrOrderCancelled, "cancelled"
	}
	if o.settled {
		return Verdict{}, ErrAlreadyPaid, "already settled"
	}
	if o.stage != StageDelivered {
		return Verdict{}, ErrNotDelivered, fmt.Sprintf("stage=%v", o.stage)
	}
	if ts < o.deliverTs+o.params.ClaimWindow {
		return Verdict{}, ErrWindowNotEnded, fmt.Sprintf("ts=%d < deliver=%d + window=%d", ts, o.deliverTs, o.params.ClaimWindow)
	}
	if o.deliverTs-n.promiseOf(o) <= 0 {
		return Verdict{}, ErrNoDelay, "delay <= 0"
	}
	top := o.params.Tiers[len(o.params.Tiers)-1].Threshold
	if o.deliverTs-n.promiseOf(o) < top {
		return Verdict{}, ErrNotTopTier, fmt.Sprintf("delay < top threshold %d", top)
	}
	v, reason := n.adjudicate(o, id, true)
	n.ledger = append(n.ledger, v)
	n.advance(ts)
	return v, nil, reason
}

func (n *naive) snapshot(id string) (Snapshot, bool) {
	o, ok := n.orders[id]
	if !ok {
		return Snapshot{}, false
	}
	ext := n.extensionOf(o)
	snap := Snapshot{
		Stage:       o.stage,
		OrigPromise: o.acceptTs + o.params.PromiseDuration,
		Promise:     o.acceptTs + o.params.PromiseDuration + ext,
		Extension:   ext,
		Cancelled:   o.cancelled,
		Settled:     o.settled,
	}
	if o.stage == StageDelivered {
		snap.Delay = o.deliverTs - snap.Promise
	}
	return snap, true
}

func errCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, ErrClockRollback):
		return "clock-rollback"
	case errors.Is(err, ErrOrderNotFound):
		return "order-not-found"
	case errors.Is(err, ErrOrderExists):
		return "order-exists"
	case errors.Is(err, ErrOrderCancelled):
		return "order-cancelled"
	case errors.Is(err, ErrEventOrder):
		return "event-order"
	case errors.Is(err, ErrAddrChanged):
		return "addr-changed"
	case errors.Is(err, ErrAlreadyPaid):
		return "already-paid"
	case errors.Is(err, ErrNotDelivered):
		return "not-delivered"
	case errors.Is(err, ErrWindowExpired):
		return "window-expired"
	case errors.Is(err, ErrWindowNotEnded):
		return "window-not-ended"
	case errors.Is(err, ErrNoDelay):
		return "no-delay"
	case errors.Is(err, ErrNotTopTier):
		return "not-top-tier"
	}
	return "unknown: " + err.Error()
}

func randomParams(rng *rand.Rand) Params {
	nTiers := 2 + rng.Intn(3)
	tiers := make([]Tier, 0, nTiers)
	threshold := int64(0)
	for i := 0; i < nTiers; i++ {
		threshold += 5 + rng.Int63n(30)
		tiers = append(tiers, Tier{Threshold: threshold, Amount: rng.Int63n(100)})
	}
	return Params{
		PromiseDuration:   30 + rng.Int63n(170),
		PrepAllowance:     rng.Int63n(30),
		DispatchAllowance: rng.Int63n(30),
		PickupAllowance:   rng.Int63n(30),
		AddressExtension:  rng.Int63n(50),
		ExtensionCap:      rng.Int63n(120),
		ClaimWindow:       20 + rng.Int63n(60),
		Tiers:             tiers,
	}
}

// TestRandomizedDifferential 随机生成操作序列，逐步对比增量实现与
// 逐笔全量重算的朴素模型，日志打印每步输入、输出与判定依据。
func TestRandomizedDifferential(t *testing.T) {
	ids := []string{"A", "B", "C", "D", "E", "F"}
	for trial := 0; trial < 25; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)*7919 + 42))
		params := randomParams(rng)
		sys, err := NewSystem(params)
		if err != nil {
			t.Fatalf("trial %d: NewSystem: %v", trial, err)
		}
		nv := newNaive(params)
		now := int64(0)
		pickID := func() string { return ids[rng.Intn(len(ids))] }

		for step := 0; step < 300; step++ {
			now += rng.Int63n(8)
			if rng.Intn(40) == 0 {
				now += 200 // 偶发时间跳跃，推开申请窗口
			}
			ts := now
			if rng.Intn(10) == 0 {
				ts -= rng.Int63n(30) // 偶发时钟回退尝试
			}

			var sysErr, nvErr error
			var sysV, nvV Verdict
			var opDesc, reason string
			isClaim := false

			switch dice := rng.Intn(100); {
			case dice < 14: // 接受订单
				id := pickID()
				opDesc = fmt.Sprintf("accept(%s)", id)
				sysErr, nvErr = sys.Accept(id, ts), nv.accept(id, ts)
			case dice < 46: // 推进正确事件或随机事件
				id := pickID()
				stage := -1
				if snap, ok := sys.Snapshot(id); ok && !snap.Cancelled && rng.Intn(2) == 0 {
					stage = int(snap.Stage)
				} else {
					stage = rng.Intn(5)
				}
				switch stage {
				case 0:
					opDesc = fmt.Sprintf("dispatch(%s)", id)
					sysErr, nvErr = sys.Dispatch(id, ts), nv.event(id, ts, StageAccepted, func(o *nOrder) { o.dispatchTs, o.stage = ts, StageDispatched })
				case 1:
					opDesc = fmt.Sprintf("mealReady(%s)", id)
					sysErr, nvErr = sys.MealReady(id, ts), nv.event(id, ts, StageDispatched, func(o *nOrder) { o.mealReadyTs, o.stage = ts, StageMealReady })
				case 2:
					opDesc = fmt.Sprintf("pickup(%s)", id)
					sysErr, nvErr = sys.Pickup(id, ts), nv.event(id, ts, StageMealReady, func(o *nOrder) { o.pickupTs, o.stage = ts, StagePickedUp })
				case 3:
					opDesc = fmt.Sprintf("deliver(%s)", id)
					sysErr = sys.Deliver(id, ts)
					nvErr = nv.event(id, ts, StagePickedUp, func(o *nOrder) { o.deliverTs, o.stage, o.deliverSeq = ts, StageDelivered, nv.seq })
				default:
					opDesc = fmt.Sprintf("deliver-early(%s)", id)
					sysErr = sys.Deliver(id, ts)
					nvErr = nv.event(id, ts, StagePickedUp, func(o *nOrder) { o.deliverTs, o.stage, o.deliverSeq = ts, StageDelivered, nv.seq })
				}
			case dice < 54: // 改址
				id := pickID()
				opDesc = fmt.Sprintf("changeAddress(%s)", id)
				sysErr, nvErr = sys.ChangeAddress(id, ts), nv.changeAddress(id, ts)
			case dice < 63: // 登记天气
				start := now - rng.Int63n(150)
				end := start + 1 + rng.Int63n(200)
				ext := rng.Int63n(60)
				wid := fmt.Sprintf("w%d-%d", trial, step)
				opDesc = fmt.Sprintf("weather(%s,[%d,%d),%d)", wid, start, end, ext)
				sysErr, nvErr = sys.RegisterWeather(wid, start, end, ext, ts), nv.registerWeather(wid, start, end, ext, ts)
			case dice < 75: // 用户申请赔付
				id := pickID()
				opDesc = fmt.Sprintf("claim(%s)", id)
				isClaim = true
				sysV, sysErr = sys.Claim(id, ts)
				nvV, nvErr, reason = nv.claim(id, ts)
			case dice < 81: // 平台自动裁决
				id := pickID()
				opDesc = fmt.Sprintf("autoSettle(%s)", id)
				isClaim = true
				sysV, sysErr = sys.AutoSettle(id, ts)
				nvV, nvErr, reason = nv.autoSettle(id, ts)
			case dice < 86: // 取消订单
				id := pickID()
				opDesc = fmt.Sprintf("cancel(%s)", id)
				sysErr, nvErr = sys.Cancel(id, ts), nv.cancel(id, ts)
			case dice < 89: // 变更构造参数
				opDesc = "updateParams"
				p := randomParams(rng)
				sysErr, nvErr = sys.UpdateParams(p, ts), nv.updateParams(p, ts)
			default: // 随机事件（大概率乱序）
				id := pickID()
				opDesc = fmt.Sprintf("random-event(%s)", id)
				sysErr, nvErr = sys.Pickup(id, ts), nv.event(id, ts, StageMealReady, func(o *nOrder) { o.pickupTs, o.stage = ts, StagePickedUp })
			}

			if errCode(sysErr) != errCode(nvErr) {
				t.Fatalf("trial %d step %d %s ts=%d: sys err=%v, naive err=%v",
					trial, step, opDesc, ts, sysErr, nvErr)
			}
			if isClaim && sysErr == nil && sysV != nvV {
				t.Fatalf("trial %d step %d %s ts=%d: sys verdict=%+v, naive verdict=%+v",
					trial, step, opDesc, ts, sysV, nvV)
			}
			if reason == "" {
				if sysErr == nil {
					reason = "accepted"
				} else {
					reason = "rejected: " + errCode(sysErr)
				}
			}
			out := errCode(sysErr)
			if isClaim && sysErr == nil {
				out = fmt.Sprintf("verdict=%+v", sysV)
			}
			t.Logf("trial=%d step=%d ts=%d op=%s => %s | basis: %s", trial, step, ts, opDesc, out, reason)

			// 每步对比全部订单的完整状态快照。
			for _, id := range ids {
				sSnap, sOK := sys.Snapshot(id)
				nSnap, nOK := nv.snapshot(id)
				if sOK != nOK || sSnap != nSnap {
					t.Fatalf("trial %d step %d op=%s: snapshot(%s) sys=%+v,%v naive=%+v,%v",
						trial, step, opDesc, id, sSnap, sOK, nSnap, nOK)
				}
			}
		}

		// 最终账目必须完全一致。
		sysLedger := sys.Ledger()
		if len(sysLedger) != len(nv.ledger) {
			t.Fatalf("trial %d: ledger len sys=%d naive=%d", trial, len(sysLedger), len(nv.ledger))
		}
		for i := range sysLedger {
			if sysLedger[i] != nv.ledger[i] {
				t.Fatalf("trial %d: ledger[%d] sys=%+v naive=%+v", trial, i, sysLedger[i], nv.ledger[i])
			}
		}
		t.Logf("trial=%d done: %d ledger entries, params=%+v", trial, len(sysLedger), params)
	}
}
