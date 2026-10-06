package cancel

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveModel 是独立编写的朴素对照实现：单 goroutine、线性扫描、
// 不共享生产代码的任何状态机/账目函数，直接按题面规则重新推导。
type naiveModel struct {
	cfg     Config
	lastNow int64
	orders  map[string]*nOrder
	logf    func(string, ...any)
}

type nOrder struct {
	amt       Amounts
	promise   int64
	stage     Stage
	rider     string
	cancelled bool
	aborts    int

	dActive   bool
	dStart    int64
	dDeadline int64
	dClaimed  bool
	dStage    Stage
	res       nResult
}

type nResult struct {
	land     bool
	liable   Party
	cash     int64
	restored int64
	cm       int64
	bears    int64
	ledger   Ledger
}

type nOutcome struct {
	errCode ErrCode
	res     nResult
	land    bool
	stage   Stage
	pending bool
	riderAb int
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, orders: map[string]*nOrder{}}
}

// settle 按题面规则独立计算拆分与四方账目（不调用生产代码 split.go）。
func (m *naiveModel) settle(o *nOrder, liable, bearer Party, full bool, bears int64, riderComp bool) nResult {
	amt := o.amt
	paid := amt.Paid()
	total := paid + amt.Coupon
	r := nResult{land: true, liable: liable}
	if full {
		r.cash = paid
		r.restored = amt.Coupon
	} else {
		if bears > amt.Goods {
			bears = amt.Goods
		}
		r.bears = bears
		r.cash = paid - bears
		if r.cash < 0 {
			r.cash = 0
		}
		if amt.Coupon > 0 {
			r.cm = amt.Coupon * bears / amt.Goods
		}
	}
	comp := int64(0)
	if riderComp {
		comp = m.cfg.RiderComp
	}
	l := Ledger{UserRefund: r.cash + r.restored, Rider: comp}
	switch {
	case liable == PartyUser:
		l.Merchant = r.bears - r.cm
	case bearer == PartyMerchant:
		penalty := int64(0)
		if liable == PartyMerchant {
			penalty = m.cfg.MerchantPenalty
		}
		l.Merchant = -l.UserRefund - penalty - comp
	}
	l.Platform = total - l.UserRefund - l.Merchant - l.Rider
	r.ledger = l
	return r
}

// flush 线性扫描所有订单令到期争议落地（朴素实现，O(订单数) 可接受）。
func (m *naiveModel) flush(now int64) {
	ids := make([]string, 0, len(m.orders))
	for id := range m.orders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := m.orders[id]
		if o.dActive && o.dDeadline <= now {
			held := o.dClaimed
			o.dActive = false
			if held {
				bears := o.amt.Goods * m.cfg.PrepLossBP / 10000
				o.res = m.settle(o, PartyUser, PartyPlatform, false, bears, false)
			} else {
				o.res = m.settle(o, PartyUserNoFault, PartyMerchant, true, 0, false)
			}
			o.cancelled = true
		}
	}
}

func (o *nOrder) snapshot() nOutcome {
	return nOutcome{
		res:     o.res,
		land:    o.cancelled,
		stage:   o.stage,
		pending: o.dActive,
		riderAb: o.aborts,
	}
}

func (m *naiveModel) create(now int64, id string, amt Amounts, promise int64) nOutcome {
	if now < m.lastNow {
		return nOutcome{errCode: ErrClockRollback}
	}
	if id == "" || amt.Goods < 0 || amt.Packing < 0 || amt.Delivery < 0 ||
		amt.Coupon < 0 || amt.Coupon > amt.Goods || amt.Paid() < 0 {
		return nOutcome{errCode: ErrInvalidParam}
	}
	if _, dup := m.orders[id]; dup {
		return nOutcome{errCode: ErrInvalidParam}
	}
	m.flush(now)
	m.lastNow = now
	m.orders[id] = &nOrder{amt: amt, promise: promise, stage: StagePaid}
	return nOutcome{stage: StagePaid}
}

func (m *naiveModel) advance(now int64, id string, to Stage, rider string) nOutcome {
	if now < m.lastNow {
		return nOutcome{errCode: ErrClockRollback}
	}
	o, ok := m.orders[id]
	if !ok {
		return nOutcome{errCode: ErrOrderNotFound}
	}
	m.flush(now)
	if o.stage == StageDelivered {
		return nOutcome{errCode: ErrDelivered}
	}
	if o.cancelled {
		return nOutcome{errCode: ErrCancelled}
	}
	if o.dActive && to == StagePicked {
		return nOutcome{errCode: ErrCancelPending}
	}
	if to != o.stage+1 {
		return nOutcome{errCode: ErrStageOrder}
	}
	m.lastNow = now
	o.stage = to
	if to == StageAssigned {
		o.rider = rider
	}
	return o.snapshot()
}

func (m *naiveModel) claim(now int64, id string) nOutcome {
	if now < m.lastNow {
		return nOutcome{errCode: ErrClockRollback}
	}
	o, ok := m.orders[id]
	if !ok {
		return nOutcome{errCode: ErrOrderNotFound}
	}
	if o.dActive && now >= o.dDeadline {
		return nOutcome{errCode: ErrClaimTimeout}
	}
	m.flush(now)
	if o.stage == StageDelivered {
		return nOutcome{errCode: ErrDelivered}
	}
	if o.cancelled {
		return nOutcome{errCode: ErrCancelled}
	}
	if !o.dActive {
		return nOutcome{errCode: ErrNoDispute}
	}
	if now < o.dStart {
		return nOutcome{errCode: ErrClaimTimeout}
	}
	m.lastNow = now
	o.dClaimed = true
	return o.snapshot()
}

func (m *naiveModel) waive(now int64, id string) nOutcome {
	if now < m.lastNow {
		return nOutcome{errCode: ErrClockRollback}
	}
	o, ok := m.orders[id]
	if !ok {
		return nOutcome{errCode: ErrOrderNotFound}
	}
	if o.dActive && now >= o.dDeadline {
		return nOutcome{errCode: ErrClaimTimeout}
	}
	m.flush(now)
	if o.stage == StageDelivered {
		return nOutcome{errCode: ErrDelivered}
	}
	if o.cancelled {
		return nOutcome{errCode: ErrCancelled}
	}
	if !o.dActive {
		return nOutcome{errCode: ErrNoDispute}
	}
	m.lastNow = now
	o.dActive = false
	o.res = m.settle(o, PartyUserNoFault, PartyMerchant, true, 0, false)
	o.cancelled = true
	return o.snapshot()
}

func (m *naiveModel) expire(now int64) nOutcome {
	if now < m.lastNow {
		return nOutcome{errCode: ErrClockRollback}
	}
	m.flush(now)
	m.lastNow = now
	return nOutcome{}
}

func (m *naiveModel) cancel(now int64, id string, actor Actor) nOutcome {
	if now < m.lastNow {
		return nOutcome{errCode: ErrClockRollback}
	}
	o, ok := m.orders[id]
	if !ok {
		return nOutcome{errCode: ErrOrderNotFound}
	}
	m.flush(now)
	if o.stage == StageDelivered {
		return nOutcome{errCode: ErrDelivered}
	}
	if o.cancelled {
		return nOutcome{errCode: ErrCancelled}
	}
	if o.dActive {
		return nOutcome{errCode: ErrCancelPending}
	}
	if actor == ActorRider {
		if o.stage == StagePicked {
			return nOutcome{errCode: ErrPickedUp}
		}
		if o.stage != StageAssigned {
			return nOutcome{errCode: ErrForbidden}
		}
		m.lastNow = now
		o.aborts++
		o.rider = ""
		o.stage = StageAccepted
		return o.snapshot()
	}
	if actor == ActorUser && o.stage == StagePicked {
		if now <= o.promise+m.cfg.LateTolerance {
			return nOutcome{errCode: ErrNotCancellable}
		}
	}
	if actor == ActorUser && (o.stage == StageAccepted || o.stage == StageAssigned) {
		m.lastNow = now
		o.dActive = true
		o.dStart = now
		o.dDeadline = now + m.cfg.DisputeWindow
		o.dClaimed = false
		o.dStage = o.stage
		m.flush(now)
		return o.snapshot()
	}
	m.lastNow = now
	var r nResult
	hasRider := o.stage == StageAssigned || o.stage == StagePicked
	switch actor {
	case ActorUser:
		// 接单前或迟到后。
		if o.stage == StagePaid {
			r = m.settle(o, PartyUserNoFault, PartyMerchant, true, 0, false)
		} else {
			r = m.settle(o, PartyPlatform, PartyPlatform, true, 0, true)
		}
	case ActorMerchant:
		r = m.settle(o, PartyMerchant, PartyMerchant, true, 0, hasRider)
	case ActorPlatform:
		r = m.settle(o, PartyPlatform, PartyPlatform, true, 0, hasRider)
	}
	o.res = r
	o.cancelled = true
	return o.snapshot()
}

// --- 随机操作序列生成与差分 ---

type opKind int

const (
	opCreate opKind = iota
	opAccept
	opAssign
	opPickup
	opDeliver
	opCancelUser
	opCancelMerchant
	opCancelPlatform
	opCancelRider
	opClaim
	opWaive
	opExpire
)

type genOp struct {
	kind opKind
	id   string
	now  int64
	amt  Amounts
	prom int64
}

func genSequence(rng *rand.Rand, cfg Config, orderCount, steps int) []genOp {
	ids := make([]string, orderCount)
	meta := make(map[string]Amounts)
	prom := make(map[string]int64)
	var ops []genOp
	now := int64(0)
	for i := range ids {
		ids[i] = fmt.Sprintf("ord%03d", i)
		g := int64(1 + rng.Intn(2000))
		pk := int64(rng.Intn(200))
		dl := int64(rng.Intn(300))
		cp := int64(rng.Intn(int(g) + 1))
		a := Amounts{Goods: g, Packing: pk, Delivery: dl, Coupon: cp}
		p := int64(300 + rng.Intn(800))
		meta[ids[i]] = a
		prom[ids[i]] = p
		ops = append(ops, genOp{kind: opCreate, id: ids[i], now: now, amt: a, prom: p})
	}
	// 后续操作：时间通常非降，小概率回退；大量跨到期窗口的操作。
	for i := 0; i < steps; i++ {
		if rng.Intn(20) == 0 && now > 0 {
			now -= int64(1 + rng.Intn(5)) // 时钟回退
		} else {
			now += int64(rng.Intn(60))
		}
		id := ids[rng.Intn(len(ids))]
		k := opKind(rng.Intn(int(opExpire) + 1))
		ops = append(ops, genOp{kind: k, id: id, now: now,
			amt: meta[id], prom: prom[id]})
	}
	return ops
}

func applyNaive(m *naiveModel, op genOp) nOutcome {
	switch op.kind {
	case opCreate:
		return m.create(op.now, op.id, op.amt, op.prom)
	case opAccept:
		return m.advance(op.now, op.id, StageAccepted, "")
	case opAssign:
		return m.advance(op.now, op.id, StageAssigned, "r-"+op.id)
	case opPickup:
		return m.advance(op.now, op.id, StagePicked, "")
	case opDeliver:
		return m.advance(op.now, op.id, StageDelivered, "")
	case opCancelUser:
		return m.cancel(op.now, op.id, ActorUser)
	case opCancelMerchant:
		return m.cancel(op.now, op.id, ActorMerchant)
	case opCancelPlatform:
		return m.cancel(op.now, op.id, ActorPlatform)
	case opCancelRider:
		return m.cancel(op.now, op.id, ActorRider)
	case opClaim:
		return m.claim(op.now, op.id)
	case opWaive:
		return m.waive(op.now, op.id)
	case opExpire:
		return m.expire(op.now)
	}
	return nOutcome{errCode: ErrInvalidParam}
}

func applyReal(s *Service, op genOp) (ErrCode, *CancelResult, Snapshot) {
	var res *CancelResult
	var err error
	var b bool
	switch op.kind {
	case opCreate:
		err = s.CreateOrder(op.now, Order{ID: op.id, Amounts: op.amt, PromiseDelivery: op.prom})
	case opAccept:
		err = s.Accept(op.now, op.id)
	case opAssign:
		err = s.Assign(op.now, op.id, "r-"+op.id)
	case opPickup:
		err = s.Pickup(op.now, op.id)
	case opDeliver:
		err = s.Deliver(op.now, op.id)
	case opCancelUser:
		res, err = s.Cancel(op.now, op.id, ActorUser)
	case opCancelMerchant:
		res, err = s.Cancel(op.now, op.id, ActorMerchant)
	case opCancelPlatform:
		res, err = s.Cancel(op.now, op.id, ActorPlatform)
	case opCancelRider:
		res, err = s.Cancel(op.now, op.id, ActorRider)
	case opClaim:
		b, err = s.Claim(op.now, op.id)
		_ = b
	case opWaive:
		res, err = s.Waive(op.now, op.id)
	case opExpire:
		_, err = s.ExpireDue(op.now)
	}
	snap, _ := s.Get(op.id)
	return Code(err), res, snap
}

// TestDifferentialRandom 随机生成操作序列，朴素模型与生产系统逐步比对：
// 错误码、落地责任方、拆分、四方账目、阶段、争议状态与骑手取消计数。
func TestDifferentialRandom(t *testing.T) {
	cfg := testCfg()
	const runs, orders, steps = 200, 6, 40
	for seed := int64(0); seed < runs; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genSequence(rng, cfg, orders, steps)
		svc := newSvc(t, cfg)
		nm := newNaive(cfg)
		var trace []string
		svc.Logf = func(f string, a ...any) {
			trace = append(trace, fmt.Sprintf(f, a...))
		}
		for i, op := range ops {
			no := applyNaive(nm, op)
			code, res, snap := applyReal(svc, op)
			trace = append(trace,
				fmt.Sprintf("STEP %d op=%s id=%s now=%d -> naive=%s real=%s",
					i, opName(op.kind), op.id, op.now, no.errCode.Name(), code.Name()))
			if code != no.errCode {
				t.Fatalf("seed=%d step=%d op=%s now=%d: error mismatch naive=%s real=%s\n%s",
					seed, i, opName(op.kind), op.now, no.errCode.Name(), code.Name(),
					lastTrace(trace, 25))
			}
			if code != 0 {
				continue
			}
			if op.kind == opExpire || op.kind == opCreate {
				continue
			}
			// 阶段 / 争议 / 骑手取消数一致。
			if snap.Stage != no.stage || snap.DisputeActive != no.pending ||
				snap.Cancelled != no.land || snap.RiderAborts != no.riderAb {
				t.Fatalf("seed=%d step=%d: state mismatch real{stage=%s pending=%v land=%v ab=%d} naive{stage=%s pending=%v land=%v ab=%d}\n%s",
					seed, i, snap.Stage, snap.DisputeActive, snap.Cancelled, snap.RiderAborts,
					no.stage, no.pending, no.land, no.riderAb, lastTrace(trace, 25))
			}
			// 落地取消：责任方与账目一致，并校验守恒。
			if no.land && no.res.land {
				nr := no.res
				if res == nil || !res.Land {
					t.Fatalf("seed=%d step=%d: naive landed but real did not", seed, i)
				}
				if res.Liable != nr.liable ||
					res.Refund.CashRefund != nr.cash ||
					res.Refund.CouponRestored != nr.restored ||
					res.Refund.CouponToMerchant != nr.cm ||
					res.Refund.UserBears != nr.bears ||
					res.Ledger != nr.ledger {
					t.Fatalf("seed=%d step=%d: ledger mismatch real=%+v naive=%+v\n%s",
						seed, i, res, nr, lastTrace(trace, 25))
				}
				sum := nr.ledger.UserRefund + nr.ledger.Merchant + nr.ledger.Rider + nr.ledger.Platform
				if sum != op.amt.Paid()+op.amt.Coupon {
					t.Fatalf("seed=%d step=%d: naive conservation broken %+v", seed, i, nr.ledger)
				}
			}
		}
	}
}

func lastTrace(t []string, n int) string {
	if len(t) > n {
		t = t[len(t)-n:]
	}
	out := ""
	for _, l := range t {
		out += l + "\n"
	}
	return out
}

func opName(k opKind) string {
	names := []string{"create", "accept", "assign", "pickup", "deliver",
		"cancel_user", "cancel_merchant", "cancel_platform", "cancel_rider",
		"claim", "waive", "expire"}
	if int(k) < len(names) {
		return names[k]
	}
	return "?"
}
