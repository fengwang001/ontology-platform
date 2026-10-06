package fencing

// 本文件包含一个独立实现的朴素对照模型：逐单元、逐事件全量扫描，
// 不做任何索引与清理。差分测试对随机操作序列逐步步进两个实现，
// 比对每步输出与订单终态，并打印每步输入、输出与判定依据。

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
)

type naiveMerchant struct {
	region     string
	cells      []Cell // 逐单元线性扫描
	baseRadius int
	level      int
}

type naiveEvent struct {
	region       string
	start, end   int64
	level        int
	terminated   bool
	terminatedAt int64
}

func (e *naiveEvent) effEnd() int64 {
	if e.terminated && e.terminatedAt < e.end {
		return e.terminatedAt
	}
	return e.end
}

type naiveOrder struct {
	merchant string
	cell     string
	state    orderState
	changed  bool
}

type naive struct {
	clock     int64
	merchants map[string]*naiveMerchant
	events    map[string]*naiveEvent // 全量历史，从不清理
	orders    map[string]*naiveOrder
}

func newNaive() *naive {
	return &naive{
		merchants: make(map[string]*naiveMerchant),
		events:    make(map[string]*naiveEvent),
		orders:    make(map[string]*naiveOrder),
	}
}

func (n *naive) checkClock(now int64) error {
	if now < n.clock {
		return newErr(ErrKindClockRollback, "time %d is before max accepted time %d", now, n.clock)
	}
	return nil
}

// levelAt 逐事件全量扫描（含早已结束的历史事件）。
func (n *naive) levelAt(m *naiveMerchant, t int64) int {
	level := m.level
	for _, ev := range n.events {
		if ev.region != m.region || ev.level <= level {
			continue
		}
		if ev.start <= t && t < ev.effEnd() {
			level = ev.level
		}
	}
	return level
}

// ringOf 逐单元线性扫描。
func (n *naive) ringOf(m *naiveMerchant, cellID string) (int, bool) {
	for _, c := range m.cells {
		if c.ID == cellID {
			return c.Ring, true
		}
	}
	return 0, false
}

func naiveRadius(m *naiveMerchant, level int) int {
	r := m.baseRadius - level
	if r < 0 {
		return 0
	}
	return r
}

// reachability 返回 0=可达 1=永久 2=暂时。
func (n *naive) reachability(m *naiveMerchant, cellID string, t int64) int {
	ring, ok := n.ringOf(m, cellID)
	if !ok {
		return 1
	}
	if ring > naiveRadius(m, n.levelAt(m, t)) {
		return 2
	}
	return 0
}

func (n *naive) registerMerchant(now int64, id, region string, cells []Cell) error {
	if id == "" || region == "" || now < 0 {
		return newErr(ErrKindInvalidParam, "bad params")
	}
	m, err := newMerchant(id, region, cells) // 复用参数校验
	if err != nil {
		return err
	}
	if _, dup := n.merchants[id]; dup {
		return newErr(ErrKindInvalidParam, "merchant %q already registered", id)
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	nm := &naiveMerchant{region: region, cells: append([]Cell(nil), cells...), baseRadius: m.baseRadius}
	n.merchants[id] = nm
	n.clock = now
	return nil
}

func (n *naive) registerEvent(now int64, id, region string, start, end int64, level int) error {
	if id == "" || region == "" || now < 0 || start < 0 || end <= start || level < 0 {
		return newErr(ErrKindInvalidParam, "bad event params")
	}
	if _, dup := n.events[id]; dup {
		return newErr(ErrKindInvalidParam, "event %q already registered", id)
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.events[id] = &naiveEvent{region: region, start: start, end: end, level: level}
	n.clock = now
	return nil
}

func (n *naive) terminateEvent(now int64, id string) error {
	if id == "" || now < 0 {
		return newErr(ErrKindInvalidParam, "bad params")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	ev, ok := n.events[id]
	if !ok {
		return newErr(ErrKindEventNotFound, "event %q not found", id)
	}
	if ev.terminated {
		return newErr(ErrKindEventTerminated, "event %q already terminated", id)
	}
	if now < ev.start {
		return newErr(ErrKindInvalidParam, "termination %d before start %d", now, ev.start)
	}
	ev.terminated = true
	ev.terminatedAt = now
	n.clock = now
	return nil
}

func (n *naive) setMerchantLevel(now int64, merchantID string, level int) error {
	if merchantID == "" || level < 0 || now < 0 {
		return newErr(ErrKindInvalidParam, "bad params")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	m, ok := n.merchants[merchantID]
	if !ok {
		return newErr(ErrKindMerchantNotFound, "merchant %q not found", merchantID)
	}
	m.level = level
	n.clock = now
	return nil
}

func (n *naive) placeOrder(now int64, orderID, merchantID, cellID string) error {
	if orderID == "" || merchantID == "" || cellID == "" || now < 0 {
		return newErr(ErrKindInvalidParam, "bad params")
	}
	if _, dup := n.orders[orderID]; dup {
		return newErr(ErrKindInvalidParam, "order %q already exists", orderID)
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	m, ok := n.merchants[merchantID]
	if !ok {
		return newErr(ErrKindMerchantNotFound, "merchant %q not found", merchantID)
	}
	switch n.reachability(m, cellID, now) {
	case 1:
		return newErr(ErrKindPermanentOutOfRange, "cell %q not in base range", cellID)
	case 2:
		return newErr(ErrKindTemporarilyUnreachable, "cell %q shrunk out of range", cellID)
	}
	n.orders[orderID] = &naiveOrder{merchant: merchantID, cell: cellID, state: stPending}
	n.clock = now
	return nil
}

func (n *naive) acceptOrder(now int64, orderID string) error {
	if orderID == "" || now < 0 {
		return newErr(ErrKindInvalidParam, "bad params")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	o, ok := n.orders[orderID]
	if !ok {
		return newErr(ErrKindOrderNotFound, "order %q not found", orderID)
	}
	switch o.state {
	case stAccepted:
		return newErr(ErrKindOrderAccepted, "order %q already accepted", orderID)
	case stDelivered:
		return newErr(ErrKindOrderDelivered, "order %q already delivered", orderID)
	case stCancelledShrink:
		return newErr(ErrKindOrderCancelled, "order %q already cancelled", orderID)
	}
	m := n.merchants[o.merchant]
	ring, ok := n.ringOf(m, o.cell)
	if !ok {
		panic("naive: invariant violated: order cell left base range")
	}
	if ring > naiveRadius(m, n.levelAt(m, now)) {
		o.state = stCancelledShrink
		n.clock = now
		return newErr(ErrKindTemporarilyUnreachable, "order %q auto-cancelled", orderID)
	}
	o.state = stAccepted
	n.clock = now
	return nil
}

func (n *naive) deliverOrder(now int64, orderID string) error {
	if orderID == "" || now < 0 {
		return newErr(ErrKindInvalidParam, "bad params")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	o, ok := n.orders[orderID]
	if !ok {
		return newErr(ErrKindOrderNotFound, "order %q not found", orderID)
	}
	switch o.state {
	case stPending:
		return newErr(ErrKindOrderNotAccepted, "order %q not accepted", orderID)
	case stDelivered:
		return newErr(ErrKindOrderDelivered, "order %q already delivered", orderID)
	case stCancelledShrink:
		return newErr(ErrKindOrderCancelled, "order %q already cancelled", orderID)
	}
	o.state = stDelivered
	n.clock = now
	return nil
}

func (n *naive) changeAddress(now int64, orderID, cellID string) error {
	if orderID == "" || cellID == "" || now < 0 {
		return newErr(ErrKindInvalidParam, "bad params")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	o, ok := n.orders[orderID]
	if !ok {
		return newErr(ErrKindOrderNotFound, "order %q not found", orderID)
	}
	switch o.state {
	case stDelivered:
		return newErr(ErrKindOrderDelivered, "order %q already delivered", orderID)
	case stCancelledShrink:
		return newErr(ErrKindOrderCancelled, "order %q already cancelled", orderID)
	}
	if o.changed {
		return newErr(ErrKindAddressChanged, "order %q address already changed", orderID)
	}
	m := n.merchants[o.merchant]
	switch n.reachability(m, cellID, now) {
	case 1:
		return newErr(ErrKindPermanentOutOfRange, "cell %q not in base range", cellID)
	case 2:
		return newErr(ErrKindTemporarilyUnreachable, "cell %q shrunk out of range", cellID)
	}
	o.cell = cellID
	o.changed = true
	n.clock = now
	return nil
}

func (n *naive) reachableQuery(merchantID, cellID string) (Reachability, error) {
	m, ok := n.merchants[merchantID]
	if !ok {
		return 0, newErr(ErrKindMerchantNotFound, "merchant %q not found", merchantID)
	}
	switch n.reachability(m, cellID, n.clock) {
	case 1:
		return UnreachablePermanent, nil
	case 2:
		return UnreachableTemporary, nil
	}
	return ReachableNow, nil
}

func (n *naive) nextRecovery(merchantID, cellID string) (Recovery, error) {
	m, ok := n.merchants[merchantID]
	if !ok {
		return Recovery{}, newErr(ErrKindMerchantNotFound, "merchant %q not found", merchantID)
	}
	ring, ok := n.ringOf(m, cellID)
	if !ok {
		return Recovery{Kind: RecoveryPermanent}, nil
	}
	threshold := m.baseRadius - ring
	if m.level > threshold {
		return Recovery{Kind: RecoveryNone}, nil
	}
	if n.levelAt(m, n.clock) <= threshold {
		return Recovery{Kind: RecoveryNow, At: n.clock}, nil
	}
	var blocking []*naiveEvent
	for _, ev := range n.events { // 逐事件全量扫描
		if ev.region == m.region && ev.level > threshold && ev.effEnd() > n.clock {
			blocking = append(blocking, ev)
		}
	}
	sort.Slice(blocking, func(i, j int) bool { return blocking[i].start < blocking[j].start })
	t := n.clock
	for _, ev := range blocking {
		if ev.effEnd() <= t {
			continue
		}
		if ev.start > t {
			break
		}
		t = ev.effEnd()
	}
	return Recovery{Kind: RecoveryAt, At: t}, nil
}

// genOp 是一条随机生成的操作。
type genOp struct {
	kind    string
	now     int64
	a, b    string
	x, y, z int64
}

func (op genOp) String() string {
	return fmt.Sprintf("%s now=%d a=%s b=%s x=%d y=%d z=%d", op.kind, op.now, op.a, op.b, op.x, op.y, op.z)
}

func errKindName(err error) string {
	if err == nil {
		return "ok"
	}
	k, _ := ErrKindOf(err)
	return k.String()
}

// runSys 在真实系统上执行操作，返回规范化输出串。
func runSys(s *System, op genOp) string {
	switch op.kind {
	case "event":
		return errKindName(s.RegisterEvent(op.now, op.a, op.b, op.x, op.y, int(op.z)))
	case "terminate":
		return errKindName(s.TerminateEvent(op.now, op.a))
	case "setLevel":
		return errKindName(s.SetMerchantLevel(op.now, op.a, int(op.x)))
	case "place":
		return errKindName(s.PlaceOrder(op.now, op.a, op.b, cellName(op.x)))
	case "accept":
		return errKindName(s.AcceptOrder(op.now, op.a))
	case "deliver":
		return errKindName(s.DeliverOrder(op.now, op.a))
	case "change":
		return errKindName(s.ChangeAddress(op.now, op.a, cellName(op.x)))
	case "queryReach":
		r, err := s.Reachable(op.a, cellName(op.x))
		return fmt.Sprintf("%s/%s", r, errKindName(err))
	case "queryRecovery":
		r, err := s.NextRecovery(op.a, cellName(op.x))
		return fmt.Sprintf("%s@%d/%s", r.Kind, r.At, errKindName(err))
	}
	panic("unknown op " + op.kind)
}

// runNaive 在朴素模型上执行同一操作。
func runNaive(n *naive, op genOp) string {
	switch op.kind {
	case "event":
		return errKindName(n.registerEvent(op.now, op.a, op.b, op.x, op.y, int(op.z)))
	case "terminate":
		return errKindName(n.terminateEvent(op.now, op.a))
	case "setLevel":
		return errKindName(n.setMerchantLevel(op.now, op.a, int(op.x)))
	case "place":
		return errKindName(n.placeOrder(op.now, op.a, op.b, cellName(op.x)))
	case "accept":
		return errKindName(n.acceptOrder(op.now, op.a))
	case "deliver":
		return errKindName(n.deliverOrder(op.now, op.a))
	case "change":
		return errKindName(n.changeAddress(op.now, op.a, cellName(op.x)))
	case "queryReach":
		r, err := n.reachableQuery(op.a, cellName(op.x))
		return fmt.Sprintf("%s/%s", r, errKindName(err))
	case "queryRecovery":
		r, err := n.nextRecovery(op.a, cellName(op.x))
		return fmt.Sprintf("%s@%d/%s", r.Kind, r.At, errKindName(err))
	}
	panic("unknown op " + op.kind)
}

func cellName(x int64) string {
	if x < 0 {
		return "ghost" // 不在任何基础范围内的单元
	}
	return fmt.Sprintf("c%d", x)
}

// TestDifferentialRandom 对随机操作序列逐步比对真实系统与朴素模型，
// 并打印每步输入、输出与判定依据（有效等级 / 有效半径 / 环距）。
func TestDifferentialRandom(t *testing.T) {
	merchants := []string{"m0", "m1", "m2"}
	regions := map[string]string{"m0": "r0", "m1": "r0", "m2": "r1"}
	for seed := uint64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rnd := rand.New(rand.NewPCG(seed, seed^0x9e3779b9))
			s := NewSystem()
			n := newNaive()
			for _, m := range merchants {
				cells := ringCells("c", 4)
				if err := s.RegisterMerchant(0, m, regions[m], cells); err != nil {
					t.Fatal(err)
				}
				if err := n.registerMerchant(0, m, regions[m], cells); err != nil {
					t.Fatal(err)
				}
			}
			var eventIDs, orderIDs []string
			now := int64(0)
			for step := 0; step < 400; step++ {
				op := genRandOp(rnd, merchants, &now, &eventIDs, &orderIDs)
				gotSys := runSys(s, op)
				gotNaive := runNaive(n, op)
				// 判定依据：m0 当前有效等级与有效半径（两实现各算一份）。
				lvlSys := s.effectiveLevelAt(s.merchants["m0"], s.clock)
				lvlNaive := n.levelAt(n.merchants["m0"], n.clock)
				radius := s.merchants["m0"].effectiveRadius(lvlSys)
				t.Logf("step=%03d op=[%s] -> sys=%s naive=%s | clock=%d level(m0): sys=%d naive=%d radius(m0)=%d",
					step, op, gotSys, gotNaive, s.clock, lvlSys, lvlNaive, radius)
				if gotSys != gotNaive {
					t.Fatalf("step %d divergence on [%s]: sys=%s naive=%s", step, op, gotSys, gotNaive)
				}
				if s.clock != n.clock {
					t.Fatalf("step %d clock divergence: sys=%d naive=%d", step, s.clock, n.clock)
				}
				compareOrders(t, step, s, n)
			}
		})
	}
}

func compareOrders(t *testing.T, step int, s *System, n *naive) {
	t.Helper()
	if len(s.orders) != len(n.orders) {
		t.Fatalf("step %d: order count sys=%d naive=%d", step, len(s.orders), len(n.orders))
	}
	for id, so := range s.orders {
		no, ok := n.orders[id]
		if !ok {
			t.Fatalf("step %d: order %s missing in naive", step, id)
		}
		if so.state != no.state || so.cell != no.cell || so.changed != no.changed {
			t.Fatalf("step %d: order %s sys=(%s,%s,%v) naive=(%s,%s,%v)",
				step, id, so.state, so.cell, so.changed, no.state, no.cell, no.changed)
		}
	}
}

// genRandOp 生成一条随机操作；时刻大体单调推进，小概率故意回退。
func genRandOp(rnd *rand.Rand, merchants []string, now *int64, eventIDs, orderIDs *[]string) genOp {
	if rnd.Int64N(100) < 5 && *now > 0 {
		*now-- // 故意时钟回退
	} else {
		*now += rnd.Int64N(4)
	}
	op := genOp{now: *now}
	pickMerchant := func() string { return merchants[rnd.IntN(len(merchants))] }
	pickCell := func() int64 {
		if rnd.Int64N(100) < 10 {
			return -1 // ghost 单元
		}
		return rnd.Int64N(6) // c0..c5（c5 超出半径 4 的基础范围？不，c5 不存在 → ghost 等价）
	}
	pickEvent := func() string {
		if len(*eventIDs) == 0 || rnd.Int64N(100) < 20 {
			return "evt-ghost"
		}
		return (*eventIDs)[rnd.IntN(len(*eventIDs))]
	}
	pickOrder := func() string {
		if len(*orderIDs) == 0 || rnd.Int64N(100) < 20 {
			return "ord-ghost"
		}
		return (*orderIDs)[rnd.IntN(len(*orderIDs))]
	}
	switch w := rnd.Int64N(100); {
	case w < 18: // 登记事件（含嵌套/重叠区间）
		op.kind = "event"
		op.a = fmt.Sprintf("e%d", len(*eventIDs))
		if rnd.Int64N(100) < 5 && len(*eventIDs) > 0 {
			op.a = (*eventIDs)[0] // 小概率重复 id
		}
		op.b = fmt.Sprintf("r%d", rnd.Int64N(2))
		op.x = *now + rnd.Int64N(8)
		op.y = op.x + 1 + rnd.Int64N(20)
		op.z = rnd.Int64N(6)
		if op.a == fmt.Sprintf("e%d", len(*eventIDs)) {
			*eventIDs = append(*eventIDs, op.a)
		}
	case w < 26: // 提前终止
		op.kind = "terminate"
		op.a = pickEvent()
	case w < 38: // 商家自设等级
		op.kind = "setLevel"
		op.a = pickMerchant()
		op.x = rnd.Int64N(7)
	case w < 58: // 下单
		op.kind = "place"
		op.a = fmt.Sprintf("o%d", len(*orderIDs))
		op.b = pickMerchant()
		op.x = pickCell()
		*orderIDs = append(*orderIDs, op.a)
	case w < 72: // 接单
		op.kind = "accept"
		op.a = pickOrder()
	case w < 80: // 送达
		op.kind = "deliver"
		op.a = pickOrder()
	case w < 88: // 改址
		op.kind = "change"
		op.a = pickOrder()
		op.x = pickCell()
	case w < 94: // 可达查询
		op.kind = "queryReach"
		op.a = pickMerchant()
		op.x = pickCell()
	default: // 恢复时刻查询
		op.kind = "queryRecovery"
		op.a = pickMerchant()
		op.x = pickCell()
	}
	return op
}
