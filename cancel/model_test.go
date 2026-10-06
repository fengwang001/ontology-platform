package cancel

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// 朴素模型：刻意不用堆，每次扫描全部订单处理窗口到期，作为独立参照实现。
// 它只复现被接受操作的最终语义（阶段、落地责任、四方账目）。
type naiveOrder struct {
	stage     Stage
	rider     string
	open      bool
	windowEnd int64
	landed    bool
	liable    Party
	ledger    Ledger
	riderCanc int
	amounts   Amounts
	promised  int64
}

type naiveModel struct {
	p      Params
	now    int64
	orders map[string]*naiveOrder
}

type op struct {
	name  string
	t     int64
	id    string
	rider string
	spec  OrderSpec
}

type opResult struct {
	ok        bool
	kind      ErrKind
	stage     Stage
	landed    bool
	liable    Party
	pending   bool
	end       int64
	ledger    Ledger
	riderC    int
	landedIDs []string
}

func newNaive(p Params) *naiveModel {
	return &naiveModel{p: p, orders: map[string]*naiveOrder{}}
}

// sweep 朴素扫描全部订单；inclusive 控制右端点取等是否到期。
func (m *naiveModel) sweep(inclusive bool) []string {
	var ids []string
	for id, o := range m.orders {
		if o.open {
			if m.now > o.windowEnd || (inclusive && m.now == o.windowEnd) {
				o.open = false
				o.windowEnd = 0
				o.landed = true
				o.liable = PartyMerchant
				o.ledger = settleLedger(o.amounts, m.p, landFullWindowExpiry, o.stage >= StageAssigned)
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func fullLedger(o *naiveOrder, p Params, liable Party) Ledger {
	assigned := o.stage >= StageAssigned
	if liable == PartyMerchant {
		return settleLedger(o.amounts, p, landFullMerchant, assigned)
	}
	return settleLedger(o.amounts, p, landFullPlatform, assigned)
}

func (m *naiveModel) run(step op) opResult {
	invalid := func(k ErrKind) opResult { return opResult{ok: false, kind: k} }
	now := step.t
	if now < 0 || step.id == "" {
		return invalid(KindInvalidParam)
	}
	if step.name == "create" {
		a := step.spec.Amounts
		if a.Goods < 0 || a.Packing < 0 || a.Delivery < 0 || a.Coupon < 0 || a.Coupon > a.Goods {
			return invalid(KindInvalidParam)
		}
		if now < m.now {
			return invalid(KindClockBack)
		}
		if _, exists := m.orders[step.id]; exists {
			return invalid(KindInvalidParam)
		}
		m.now = now
		m.orders[step.id] = &naiveOrder{stage: StagePaid, amounts: a, promised: step.spec.PromisedTime}
		return m.snap(step.id)
	}
	if now < m.now {
		return invalid(KindClockBack)
	}
	if step.name == "advance" {
		m.now = now
		ids := m.sweep(true)
		return opResult{ok: true, landedIDs: ids}
	}
	o, exists := m.orders[step.id]
	if !exists {
		return invalid(KindNoOrder)
	}
	m.now = now
	m.sweep(false)

	switch step.name {
	case "accept":
		if o.landed || o.stage == StageDelivered {
			return invalid(KindTerminal)
		}
		if o.stage != StagePaid {
			return invalid(KindStageOrder)
		}
		o.stage = StageAccepted
	case "assign":
		if step.rider == "" {
			return invalid(KindInvalidParam)
		}
		if o.landed || o.stage == StageDelivered {
			return invalid(KindTerminal)
		}
		if o.stage != StageAccepted {
			return invalid(KindStageOrder)
		}
		o.stage = StageAssigned
		o.rider = step.rider
	case "pickup":
		if o.landed || o.stage == StageDelivered {
			return invalid(KindTerminal)
		}
		if o.stage != StageAssigned {
			return invalid(KindStageOrder)
		}
		if o.open {
			return invalid(KindCancelPending)
		}
		if step.rider == "" || step.rider != o.rider {
			return invalid(KindUnauthorized)
		}
		o.stage = StagePicked
	case "deliver":
		if o.landed || o.stage == StageDelivered {
			return invalid(KindTerminal)
		}
		if o.stage != StagePicked {
			return invalid(KindStageOrder)
		}
		if step.rider == "" || step.rider != o.rider {
			return invalid(KindUnauthorized)
		}
		o.stage = StageDelivered
	case "user", "merchant", "platform":
		if o.landed || o.stage == StageDelivered {
			return invalid(KindTerminal)
		}
		if o.open {
			return invalid(KindCancelPending)
		}
		if step.name == "user" && o.stage == StagePicked {
			if now <= o.promised+m.p.LateTolerance {
				return invalid(KindNotCancellable)
			}
			o.landed = true
			o.liable = PartyPlatform
			o.ledger = fullLedger(o, m.p, PartyPlatform)
			break
		}
		if step.name == "user" && o.stage == StagePaid {
			o.landed = true
			o.liable = PartyPlatform
			o.ledger = settleLedger(o.amounts, m.p, landFullPreAccept, false)
			break
		}
		if step.name == "user" {
			o.open = true
			o.windowEnd = now + m.p.DisputeWindow
		} else {
			o.landed = true
			if step.name == "merchant" {
				o.liable = PartyMerchant
			} else {
				o.liable = PartyPlatform
			}
			o.ledger = fullLedger(o, m.p, o.liable)
		}
	case "rider":
		if step.rider == "" {
			return invalid(KindInvalidParam)
		}
		if o.landed || o.stage == StageDelivered {
			return invalid(KindTerminal)
		}
		if o.open {
			return invalid(KindCancelPending)
		}
		if o.stage == StagePicked {
			return invalid(KindPickedUp)
		}
		if o.stage != StageAssigned || step.rider != o.rider {
			return invalid(KindUnauthorized)
		}
		o.stage = StageAccepted
		o.rider = ""
		o.riderCanc++
	case "claim":
		if o.landed || o.stage == StageDelivered {
			return invalid(KindTerminal)
		}
		if !o.open {
			return invalid(KindNoDispute)
		}
		if now >= o.windowEnd {
			return invalid(KindWindowTimeout)
		}
		o.open = false
		o.windowEnd = 0
		o.landed = true
		o.liable = PartyUser
		o.ledger = settleLedger(o.amounts, m.p, landUserFault, false)
	case "waive":
		if o.landed || o.stage == StageDelivered {
			return invalid(KindTerminal)
		}
		if !o.open {
			return invalid(KindNoDispute)
		}
		o.open = false
		o.windowEnd = 0
		o.landed = true
		o.liable = PartyMerchant
		o.ledger = settleLedger(o.amounts, m.p, landFullWindowExpiry, o.stage >= StageAssigned)
	}
	return m.snap(step.id)
}

func (m *naiveModel) snap(id string) opResult {
	o := m.orders[id]
	return opResult{ok: true, stage: o.stage, landed: o.landed, liable: o.liable,
		pending: o.open, end: o.windowEnd, ledger: o.ledger, riderC: o.riderCanc}
}

func TestNaiveDifferential(t *testing.T) {
	p := Params{DisputeWindow: 10, LateTolerance: 5, LossBasisPoints: 3000,
		MerchantPenalty: 500, RiderCompensation: 200}
	rng := rand.New(rand.NewSource(20261006))
	var logb strings.Builder
	fmt.Fprintln(&logb, "seed=20261006 params={window:10,tolerance:5,bp:3000,penalty:500,comp:200}")

	apply := func(sys *System, step op) (*Result, []string, error) {
		switch step.name {
		case "create":
			r, e := sys.CreateOrder(step.t, step.id, step.spec)
			return r, nil, e
		case "accept":
			r, e := sys.Accept(step.t, step.id)
			return r, nil, e
		case "assign":
			r, e := sys.Assign(step.t, step.id, step.rider)
			return r, nil, e
		case "pickup":
			r, e := sys.Pickup(step.t, step.id, step.rider)
			return r, nil, e
		case "deliver":
			r, e := sys.Deliver(step.t, step.id, step.rider)
			return r, nil, e
		case "user":
			r, e := sys.UserCancel(step.t, step.id)
			return r, nil, e
		case "merchant":
			r, e := sys.MerchantCancel(step.t, step.id)
			return r, nil, e
		case "platform":
			r, e := sys.PlatformCancel(step.t, step.id)
			return r, nil, e
		case "rider":
			r, e := sys.RiderCancel(step.t, step.id, step.rider)
			return r, nil, e
		case "claim":
			r, e := sys.ClaimPreparation(step.t, step.id)
			return r, nil, e
		case "waive":
			r, e := sys.WaiveClaim(step.t, step.id)
			return r, nil, e
		case "advance":
			rs, e := sys.Advance(step.t)
			var ids []string
			for _, r := range rs {
				if r.Landed {
					ids = append(ids, r.OrderID)
				}
			}
			return nil, ids, e
		}
		return nil, nil, fmt.Errorf("unknown op %s", step.name)
	}

	for trial := 0; trial < 300; trial++ {
		sys, _ := NewSystem(p)
		nm := newNaive(p)
		now := int64(0)
		n := 1 + rng.Intn(3)
		ids := make([]string, n)
		specs := map[string]OrderSpec{}
		for i := range ids {
			id := fmt.Sprintf("t%do%d", trial, i)
			ids[i] = id
			g := int64(rng.Intn(5000) + 1)
			c := int64(rng.Intn(int(g) + 1))
			specs[id] = OrderSpec{
				Amounts: Amounts{
					Goods:    g,
					Packing:  int64(rng.Intn(500)),
					Delivery: int64(rng.Intn(500)),
					Coupon:   c,
				},
				PromisedTime: int64(rng.Intn(40)) + 5,
			}
		}
		names := []string{"create", "accept", "assign", "pickup", "deliver",
			"user", "merchant", "platform", "rider", "claim", "waive", "advance"}
		for sidx := 0; sidx < 30; sidx++ {
			id := ids[rng.Intn(len(ids))]
			if rng.Intn(8) != 0 {
				now += int64(rng.Intn(4))
			} else if now > 0 {
				now--
			}
			step := op{name: names[rng.Intn(len(names))], t: now, id: id,
				rider: []string{"r1", "r2", "rX"}[rng.Intn(3)], spec: specs[id]}
			nr := nm.run(step)
			sr, sysExpired, serr := apply(sys, step)
			basis := fmt.Sprintf("trial=%d step=%d op=%s t=%d id=%s rider=%s",
				trial, sidx, step.name, step.t, step.id, step.rider)

			if step.name == "advance" {
				if (serr != nil) != !nr.ok {
					t.Fatalf("%s advance error mismatch sys=%v naive=%+v\n%s", basis, serr, nr, logb.String())
				}
				sortStrings(sysExpired)
				sortStrings(nr.landedIDs)
				fmt.Fprintf(&logb, "%s => advance expired=%v\n", basis, nr.landedIDs)
				if !reflect.DeepEqual(sysExpired, nr.landedIDs) {
					t.Fatalf("%s expiry mismatch sys=%v naive=%v\n%s", basis, sysExpired, nr.landedIDs, logb.String())
				}
				for _, xid := range nr.landedIDs {
					l := nm.orders[xid].ledger
					if !l.Conserved(nm.orders[xid].amounts) {
						t.Fatalf("%s conservation broken naive", basis)
					}
				}
				continue
			}

			if (serr != nil) != !nr.ok {
				fmt.Fprintf(&logb, "%s => sys-err=%v naive=%+v MISMATCH\n", basis, serr, nr)
				t.Fatalf("%s acceptance mismatch sys=%v naive=%+v\n%s", basis, serr, nr, logb.String())
			}
			if serr != nil {
				if errKind(serr) != nr.kind {
					t.Fatalf("%s error kind mismatch sys=%d naive=%d\n%s", basis, errKind(serr), nr.kind, logb.String())
				}
				fmt.Fprintf(&logb, "%s => rejected kind=%d\n", basis, nr.kind)
				continue
			}
			if sr.Stage != nr.stage || sr.Landed != nr.landed || sr.Liable != nr.liable ||
				sr.Pending != nr.pending || sr.WindowEnd != nr.end ||
				sr.RiderCancellations != nr.riderC || sr.Ledger != nr.ledger {
				fmt.Fprintf(&logb, "%s => sys={stage=%v landed=%v liable=%v pending=%v end=%d rc=%d ledger=%+v}\n",
					basis, sr.Stage, sr.Landed, sr.Liable, sr.Pending, sr.WindowEnd, sr.RiderCancellations, sr.Ledger)
				fmt.Fprintf(&logb, "%s => nai={stage=%v landed=%v liable=%v pending=%v end=%d rc=%d ledger=%+v} MISMATCH\n",
					basis, nr.stage, nr.landed, nr.liable, nr.pending, nr.end, nr.riderC, nr.ledger)
				t.Fatalf("%s state mismatch\n%s", basis, logb.String())
			}
			if sr.Landed && !sr.Ledger.Conserved(specs[id].Amounts) {
				t.Fatalf("%s conservation broken", basis)
			}
			fmt.Fprintf(&logb, "%s => stage=%v landed=%v liable=%v pending=%v reason=%q\n",
				basis, nr.stage, nr.landed, nr.liable, nr.pending, sr.Reason)
		}
	}
	t.Logf("differential trace:\n%s", logb.String())
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
