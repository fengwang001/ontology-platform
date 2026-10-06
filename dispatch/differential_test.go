package dispatch

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// rngTravel 是随机差分测试使用的确定性耗时源：
// 小范围点位 + 固定种子的稠密矩阵，少量边缺失（ok=false）。
type rngTravel struct {
	dist    map[[2]Location]int64
	missing map[[2]Location]bool
}

func newRNGTravel(r *rand.Rand, locs []Location, missRate float64) *rngTravel {
	m := &rngTravel{dist: map[[2]Location]int64{}, missing: map[[2]Location]bool{}}
	for _, a := range locs {
		for _, b := range locs {
			k := [2]Location{a, b}
			if a == b {
				m.dist[k] = 0
				continue
			}
			if r.Float64() < missRate {
				m.missing[k] = true
				continue
			}
			// 非对称耗时，刻意制造不满足三角不等式的情形。
			m.dist[k] = int64(r.Intn(30))
		}
	}
	return m
}

func (m *rngTravel) Travel(a, b Location) (int64, bool) {
	k := [2]Location{a, b}
	if m.missing[k] {
		return 0, false
	}
	if d, ok := m.dist[k]; ok {
		return d, true
	}
	return 0, false
}

type genState struct {
	r       *rand.Rand
	locs    []Location
	regions []Location
	orders  []OrderID
	riders  []RiderID
	clock   int64
	seq     int
}

func (g *genState) regionOf(l Location) Location { return g.regions[int(l[1]-'0')%len(g.regions)] }

// genOps 生成一段单调时钟下的随机操作，混合合法与各类非法操作。
func (g *genState) genOps(n int) []NaiveOp {
	ops := make([]NaiveOp, 0, n)
	for i := 0; i < n; i++ {
		g.clock += int64(g.r.Intn(3))
		at := g.clock
		roll := g.r.Float64()
		switch {
		case len(g.riders) < 2 || roll < 0.15:
			id := RiderID(fmt.Sprintf("rider-%d", g.seq))
			g.seq++
			region := g.regions[g.r.Intn(len(g.regions))]
			pos := g.locs[g.r.Intn(len(g.locs))]
			ops = append(ops, NaiveOp{
				Kind: "register", At: at,
				Rider: Rider{ID: id, Region: region, Capacity: 1 + g.r.Intn(3), Pos: pos, DepartedAt: at},
			})
			g.riders = append(g.riders, id)
		case roll < 0.55:
			id := OrderID(fmt.Sprintf("order-%d", g.seq))
			g.seq++
			pk := g.locs[g.r.Intn(len(g.locs))]
			dk := g.locs[g.r.Intn(len(g.locs))]
			ready := at + int64(g.r.Intn(20))
			promise := ready + int64(g.r.Intn(120))
			ops = append(ops, NaiveOp{Kind: "submit", At: at, Order: Order{ID: id, Pickup: pk, Dropoff: dk, ReadyAt: ready, Promise: promise}})
			g.orders = append(g.orders, id)
		case roll < 0.75 && len(g.orders) > 0:
			oid := g.orders[g.r.Intn(len(g.orders))]
			ops = append(ops, NaiveOp{Kind: "dispatch", At: at, OrderID: oid})
		case roll < 0.9 && len(g.orders) > 0:
			oid := g.orders[g.r.Intn(len(g.orders))]
			ops = append(ops, NaiveOp{Kind: "cancel", At: at, OrderID: oid})
		case len(g.riders) > 0 && len(g.orders) > 0:
			rid := g.riders[g.r.Intn(len(g.riders))]
			oid := g.orders[g.r.Intn(len(g.orders))]
			kind := StopPickup
			if g.r.Intn(2) == 0 {
				kind = StopDropoff
			}
			ops = append(ops, NaiveOp{Kind: "complete", At: at, RiderID: rid, OrderID: oid, StopKind: kind})
		default:
			ops = append(ops, NaiveOp{Kind: "dispatch", At: at, OrderID: OrderID(fmt.Sprintf("ghost-%d", g.seq))})
		}
		// 偶发注入时钟回退与参数非法，验证拒绝次序不变性。
		if g.r.Float64() < 0.05 && len(ops) > 0 {
			ops = append(ops, NaiveOp{Kind: "cancel", At: at - 100, OrderID: "any"})
			g.clock = at
			i++
		}
	}
	return ops
}

// runMain 用主实现重放同一操作序列，并保留调度器用于终态对比。
func runMain(tt TravelTimeSource, cfg Config, ops []NaiveOp) (*Dispatcher, []NaiveOutcome) {
	d := NewDispatcher(tt, cfg)
	out := make([]NaiveOutcome, len(ops))
	for i, op := range ops {
		switch op.Kind {
		case "register":
			out[i] = NaiveOutcome{Err: d.RegisterRider(op.At, op.Rider)}
		case "online":
			out[i] = NaiveOutcome{Err: d.SetOnline(op.At, op.RiderID, op.Online)}
		case "submit":
			out[i] = NaiveOutcome{Err: d.SubmitOrder(op.At, op.Order)}
		case "dispatch":
			res, err := d.DispatchOrder(op.At, op.OrderID)
			out[i] = NaiveOutcome{Err: err, Dispatch: res}
		case "complete":
			_, err := d.CompleteStop(op.At, op.RiderID, op.OrderID, op.StopKind)
			out[i] = NaiveOutcome{Err: err}
		case "cancel":
			out[i] = NaiveOutcome{Err: d.CancelOrder(op.At, op.OrderID)}
		}
		out[i].OK = out[i].Err == nil
	}
	return d, out
}

func sameResult(a, b NaiveOutcome) bool {
	if (a.Err == nil) != (b.Err == nil) {
		return false
	}
	if a.Err != nil && a.Err.Error() != b.Err.Error() {
		return false
	}
	if a.Err == nil {
		if a.Dispatch.RiderID != b.Dispatch.RiderID ||
			a.Dispatch.PickupIndex != b.Dispatch.PickupIndex ||
			a.Dispatch.DropoffIndex != b.Dispatch.DropoffIndex ||
			a.Dispatch.Arrival != b.Dispatch.Arrival ||
			a.Dispatch.OldExtra != b.Dispatch.OldExtra ||
			a.Dispatch.ExtraTime != b.Dispatch.ExtraTime {
			return false
		}
	}
	return true
}

func explain(ins Insertion) string {
	return fmt.Sprintf("pi=%d di=%d arrival=%d oldExtra=%d extra=%d",
		ins.PickupIndex, ins.DropoffIndex, ins.NewArrival, ins.OldExtra, ins.ExtraTime)
}

// TestRandomDifferential 以多个种子生成随机操作序列，要求主实现与独立朴素模型
// 在每一步的接受/拒绝、错误种类、选中骑手、位置、推定时刻上完全一致。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential fuzz in -short mode")
	}
	const seeds = 60
	const steps = 80
	var logAll strings.Builder

	for seed := int64(0); seed < seeds; seed++ {
		r := rand.New(rand.NewSource(seed))
		locs := []Location{"L0", "L1", "L2", "L3", "L4", "L5"}
		regions := []Location{"L0", "L1"} // 区域是位置的一种映射分组
		tt := newRNGTravel(r, locs, 0.12)
		g := &genState{r: r, locs: locs, regions: regions}
		ops := g.genOps(steps)
		cfg := Config{PickupDwell: int64(r.Intn(3)), DropDwell: int64(r.Intn(3)), MaxDetour: int64(r.Intn(25)), RegionOf: g.regionOf}

		mainD, mainOut := runMain(tt, cfg, ops)
		naive := NewNaiveModel(tt, cfg)
		naiveOut := naive.Replay(ops)

		fmt.Fprintf(&logAll, "===== seed=%d cfg=%+v steps=%d =====\n", seed, cfg, len(ops))
		for i, op := range ops {
			why := "-"
			if op.Kind == "dispatch" && mainOut[i].Err == nil {
				why = explain(Insertion{
					PickupIndex:  mainOut[i].Dispatch.PickupIndex,
					DropoffIndex: mainOut[i].Dispatch.DropoffIndex,
					NewArrival:   mainOut[i].Dispatch.Arrival,
					OldExtra:     mainOut[i].Dispatch.OldExtra,
					ExtraTime:    mainOut[i].Dispatch.ExtraTime,
				})
			}
			if op.Kind == "dispatch" && mainOut[i].Err != nil {
				why = classifyReason(mainOut[i].Err)
			}
			fmt.Fprintf(&logAll, "step=%02d op=%s at=%d rider=%q order=%q -> ok=%v err=%v %s\n",
				i, op.Kind, op.At, firstNonEmpty(op.RiderID, op.Rider.ID), firstNonEmpty(op.OrderID, op.Order.ID),
				mainOut[i].OK, errText(mainOut[i].Err), why)
			if !sameResult(mainOut[i], naiveOut[i]) {
				t.Fatalf("seed=%d step=%d op=%+v\nmain = %+v\nnaive= %+v\n\nlog:\n%s",
					seed, i, op, mainOut[i], naiveOut[i], logAll.String())
			}
		}

		// 终态对比：时钟、所有订单状态/归属、所有骑手序列与推定。
		if mainD.Now() != naive.Clock() {
			t.Fatalf("seed=%d clock mismatch: main=%d naive=%d", seed, mainD.Now(), naive.Clock())
		}
		for _, oid := range g.orders {
			mainSt, ok1 := statusViaOutcomes(ops, mainOut, oid)
			nSt, nRider, ok2 := naive.OrderState(oid)
			if ok1 != ok2 {
				t.Fatalf("seed=%d order %s existence %v/%v", seed, oid, ok1, ok2)
			}
			if ok1 && (mainSt.status != nSt || mainSt.rider != nRider) {
				t.Fatalf("seed=%d order %s state main=(%v,%q) naive=(%v,%q)",
					seed, oid, mainSt.status, mainSt.rider, nSt, nRider)
			}
		}
		// 骑手最终序列逐条对比（主实现快照 vs 朴素模型）。
		riders := append([]RiderID(nil), g.riders...)
		sort.Strings(riders)
		for _, rid := range riders {
			if !ridersAccepted(ops, mainOut, rid) {
				continue
			}
			mr, ok1 := mainD.RiderSnapshot(rid)
			nr, ns, ok2 := naive.RiderCopy(rid)
			if ok1 != ok2 {
				t.Fatalf("seed=%d rider %s existence %v/%v", seed, rid, ok1, ok2)
			}
			if !ok1 {
				continue
			}
			if mr.Pos != nr.Pos || mr.DepartedAt != nr.DepartedAt || len(mr.Pending) != len(nr.Pending) {
				t.Fatalf("seed=%d rider %s anchor/len main=(%s,%d,%d) naive=(%s,%d,%d)",
					seed, rid, mr.Pos, mr.DepartedAt, len(mr.Pending), nr.Pos, nr.DepartedAt, len(nr.Pending))
			}
			for k := range mr.Pending {
				if mr.Pending[k] != nr.Pending[k] {
					t.Fatalf("seed=%d rider %s stop %d main=%+v naive=%+v",
						seed, rid, k, mr.Pending[k], nr.Pending[k])
				}
			}
			ms, _ := mainD.RiderSchedule(rid)
			if len(ms) != len(ns) {
				t.Fatalf("seed=%d rider %s sched len", seed, rid)
			}
			for k := range ms {
				if ms[k] != ns[k] {
					t.Fatalf("seed=%d rider %s eta %d main=%+v naive=%+v",
						seed, rid, k, ms[k], ns[k])
				}
			}
		}
	}
	t.Log("\n" + logAll.String())
}

type orderFinal struct {
	status OrderStatus
	rider  RiderID
}

// statusViaOutcomes 用与主实现一致的状态机重放单笔订单，得到其最终状态/归属。
func statusViaOutcomes(ops []NaiveOp, out []NaiveOutcome, oid OrderID) (orderFinal, bool) {
	var st OrderStatus
	var rider RiderID
	existed := false
	for i, op := range ops {
		if out[i].Err != nil {
			continue
		}
		switch op.Kind {
		case "submit":
			if op.Order.ID == oid {
				existed = true
				st = OrderNew
			}
		case "dispatch":
			if op.OrderID == oid {
				st = OrderAssigned
				rider = out[i].Dispatch.RiderID
			}
		case "complete":
			if op.OrderID == oid && op.RiderID == rider {
				if op.StopKind == StopPickup {
					st = OrderPicked
				} else {
					st = OrderDelivered
				}
			}
		case "cancel":
			if op.OrderID == oid {
				st = OrderCancelled
				rider = ""
			}
		}
	}
	return orderFinal{status: st, rider: rider}, existed
}

func ridersAccepted(ops []NaiveOp, out []NaiveOutcome, rid RiderID) bool {
	for i, op := range ops {
		if op.Kind == "register" && op.Rider.ID == rid && out[i].Err == nil {
			return true
		}
	}
	return false
}

func classifyReason(err error) string {
	switch err {
	case nil:
		return "-"
	case ErrNoRiderRegionCapacity:
		return "reason=region-capacity-full"
	case ErrNewOrderPromise:
		return "reason=new-order-promise"
	case ErrExistingViolation:
		return "reason=existing-promise-or-detour"
	}
	return "reason=other"
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
