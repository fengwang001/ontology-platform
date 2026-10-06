package railway

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

type diffOp struct {
	kind                    string
	now                     int
	from, to                int
	passenger               string
	wantStanding            bool
	ticketID                int64
	badTrain, badRange, neg bool
}

func TestDifferential(t *testing.T) {
	for seed := int64(1); seed <= 800; seed++ {
		rng := rand.New(rand.NewSource(seed))
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			n := 2 + rng.Intn(4)
			cars := 1 + rng.Intn(2)
			spc := 1 + rng.Intn(2)
			deps := make([]int, n)
			d := 30 + rng.Intn(60)
			for i := range deps {
				deps[i] = d
				d += 40 + rng.Intn(80)
			}
			alloc := make([][]int, n)
			for i := range alloc {
				alloc[i] = make([]int, n)
				for j := i + 1; j < n; j++ {
					alloc[i][j] = rng.Intn(3)
				}
			}
			spec := TrainSpec{ID: "T", Departures: deps, Cars: cars, SeatsPerCar: spc,
				Alloc: alloc, Shared: rng.Intn(3)}
			cfg := Config{AdvanceSeconds: rng.Intn(40), StandingRatio: []float64{0, 0.25, 0.5, 1}[rng.Intn(4)]}

			svc := NewService(cfg)
			if err := svc.AddTrain(spec); err != nil {
				t.Fatalf("seed %d addtrain: %v", seed, err)
			}
			nm := newNaive(cfg, spec)

			now := 0
			var liveIDs []int64
			steps := 120
			for step := 0; step < steps; step++ {
				op := diffOp{}
				switch {
				case len(liveIDs) > 0 && rng.Intn(3) == 0:
					op.kind = "refund"
					op.ticketID = liveIDs[rng.Intn(len(liveIDs))]
				default:
					op.kind = "buy"
					op.from = rng.Intn(n - 1)
					op.to = op.from + 1 + rng.Intn(n-1-op.from)
					op.passenger = fmt.Sprintf("p%d", rng.Intn(5))
					op.wantStanding = rng.Intn(2) == 0
				}
				// 时间以“不降”为主，偶尔回退以触发时钟回退；个别非法输入。
				if rng.Intn(8) == 0 {
					op.now = now - 1 - rng.Intn(20)
				} else {
					now += rng.Intn(25)
					op.now = now
				}
				if rng.Intn(20) == 0 {
					op.badTrain = true
				}
				if rng.Intn(25) == 0 {
					op.from = rng.Intn(n)
					op.to = rng.Intn(n)
					op.badRange = true
				}

				if op.kind == "buy" {
					tid := "T"
					if op.badTrain {
						tid = "GHOST"
					}
					desc := TicketDesc{From: op.from, To: op.to, Passenger: op.passenger}
					res, err := svc.Buy(op.now, tid, desc, op.wantStanding)
					basis := fmt.Sprintf("seed=%d step=%d BUY now=%d %s %d->%d p=%s ws=%v",
						seed, step, op.now, tid, op.from, op.to, op.passenger, op.wantStanding)
					var na naiveOutcome
					if op.badTrain {
						// 拒绝次序：参数非法（静态可判部分）> 时钟回退 > 列车不存在。
						if op.now < 0 || op.from < 0 || op.from >= op.to {
							na = naiveOutcome{reason: ReasonInvalidParam}
						} else if op.now < nm.clock {
							na = naiveOutcome{reason: ReasonClockRollback}
						} else {
							na = naiveOutcome{reason: ReasonTrainNotFound}
						}
					} else {
						na = nm.buy(op.now, desc)
					}
					gotReason := reasonOf(err)
					t.Logf("%s => real=%v seat=%d-%d naive=%v seat=%d-%d",
						basis, gotReason, seatOf(res), seatNoOf(res),
						na.reasonOrOK(), na.car, na.no)
					if gotReason != na.reasonOrOK() {
						t.Fatalf("REASON MISMATCH: %s real=%v naive=%v", basis, gotReason, na.reasonOrOK())
					}
					if err == nil {
						if res.Ticket.Standing != na.standing ||
							(!na.standing && (res.Ticket.Car != na.car || res.Ticket.No != na.no)) ||
							res.Ticket.QuotaShared != na.useShared {
							t.Fatalf("DETAIL MISMATCH: %s real={st:%v seat:%d-%d shared:%v} naive={st:%v seat:%d-%d shared:%v}",
								basis, res.Ticket.Standing, res.Ticket.Car, res.Ticket.No, res.Ticket.QuotaShared,
								na.standing, na.car, na.no, na.useShared)
						}
						if res.Ticket.ID != na.id {
							t.Fatalf("ticket id divergence: %d vs %d", res.Ticket.ID, na.id)
						}
						liveIDs = append(liveIDs, res.Ticket.ID)
					}
				} else {
					// 朴素模型与服务用同一 id 空间（购票成功顺序一致），直接用其 id。
					err := svc.Refund(op.now, op.ticketID)
					rr := nm.refund(op.now, op.ticketID)
					t.Logf("seed=%d step=%d REFUND now=%d id=%d => real=%v naive=%v",
						seed, step, op.now, op.ticketID, reasonOf(err), rr)
					if reasonOf(err) != rr {
						t.Fatalf("REFUND MISMATCH seed=%d now=%d id=%d real=%v naive=%v",
							seed, op.now, op.ticketID, reasonOf(err), rr)
					}
				}
			}

			// 终态票额视图与朴素重算一致。
			for f := 0; f < n; f++ {
				for to := f + 1; to < n; to++ {
					merged := nm.mergedUpTo(now)
					wantShared := nm.sharedRem(merged)
					v, err := svc.Remaining(now, "T", f, to)
					if err != nil {
						t.Fatal(err)
					}
					if v.Shared != wantShared || (!v.Merged && v.Allocation != nm.allocRem(merged, f, to)) {
						t.Fatalf("final quota mismatch seed=%d %d->%d real=%+v naive shared=%d alloc=%d",
							seed, f, to, v, wantShared, nm.allocRem(merged, f, to))
					}
				}
			}
			if math.IsNaN(cfg.StandingRatio) {
				t.Fatal("impossible")
			}
		})
	}
}

func seatOf(r *BuyResult) int {
	if r == nil {
		return 0
	}
	return r.Ticket.Car
}

func seatNoOf(r *BuyResult) int {
	if r == nil {
		return 0
	}
	return r.Ticket.No
}

func (o naiveOutcome) reasonOrOK() Reason {
	if o.ok {
		return ReasonOK
	}
	return o.reason
}
