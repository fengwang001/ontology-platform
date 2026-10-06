package fence

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// opKind enumerates every operation exercised by the differential test.
type opKind int

const (
	opRegister opKind = iota
	opMerchantLevel
	opAddEvent
	opTerminate
	opPlace
	opAccept
	opReroute
	opDeliver
	opQuery
	opRecovery
)

type diffOp struct {
	kind       opKind
	t          int64
	id         string
	region     string
	cell       Cell
	start, end int64
	level      int
	at         int64
}

type refModel interface {
	RegisterMerchant(t int64, id, region string, cells map[Cell]int) error
	SetMerchantLevel(t int64, id string, level int) error
	AddPlatformEvent(t int64, id, region string, start, end int64, level int) error
	TerminateEvent(t int64, id string, at int64) error
	PlaceOrder(t int64, id, merchant string, cell Cell) error
	AcceptOrder(t int64, id string) error
	RerouteOrder(t int64, id string, cell Cell) error
	DeliverOrder(t int64, id string) error
	QueryReachable(t int64, merchant string, cell Cell) (Reachability, error)
	QueryNextRecovery(t int64, merchant string, cell Cell) (RecoveryInfo, error)
	GetOrder(id string) (OrderInfo, error)
}

func (o diffOp) String() string {
	switch o.kind {
	case opRegister:
		return fmt.Sprintf("RegisterMerchant(t=%d id=%s region=%s rings=0..%d)", o.t, o.id, o.region, o.level)
	case opMerchantLevel:
		return fmt.Sprintf("SetMerchantLevel(t=%d m=%s level=%d)", o.t, o.id, o.level)
	case opAddEvent:
		return fmt.Sprintf("AddPlatformEvent(t=%d id=%s region=%s [%d,%d) level=%d)", o.t, o.id, o.region, o.start, o.end, o.level)
	case opTerminate:
		return fmt.Sprintf("TerminateEvent(t=%d id=%s at=%d)", o.t, o.id, o.at)
	case opPlace:
		return fmt.Sprintf("PlaceOrder(t=%d id=%s m=%s cell=(%d,%d))", o.t, o.id, o.region, o.cell.X, o.cell.Y)
	case opAccept:
		return fmt.Sprintf("AcceptOrder(t=%d id=%s)", o.t, o.id)
	case opReroute:
		return fmt.Sprintf("RerouteOrder(t=%d id=%s cell=(%d,%d))", o.t, o.id, o.cell.X, o.cell.Y)
	case opDeliver:
		return fmt.Sprintf("DeliverOrder(t=%d id=%s)", o.t, o.id)
	case opQuery:
		return fmt.Sprintf("QueryReachable(t=%d m=%s cell=(%d,%d))", o.t, o.id, o.cell.X, o.cell.Y)
	case opRecovery:
		return fmt.Sprintf("QueryNextRecovery(t=%d m=%s cell=(%d,%d))", o.t, o.id, o.cell.X, o.cell.Y)
	}
	return "?"
}

// apply runs one op against a model and returns a comparable textual result.
func applyOp(m refModel, o diffOp) string {
	var err error
	switch o.kind {
	case opRegister:
		err = m.RegisterMerchant(o.t, o.id, o.region, rings(int64(o.level)+1))
	case opMerchantLevel:
		err = m.SetMerchantLevel(o.t, o.id, o.level)
	case opAddEvent:
		err = m.AddPlatformEvent(o.t, o.id, o.region, o.start, o.end, o.level)
	case opTerminate:
		err = m.TerminateEvent(o.t, o.id, o.at)
	case opPlace:
		err = m.PlaceOrder(o.t, o.id, o.region, o.cell)
	case opAccept:
		err = m.AcceptOrder(o.t, o.id)
	case opReroute:
		err = m.RerouteOrder(o.t, o.id, o.cell)
	case opDeliver:
		err = m.DeliverOrder(o.t, o.id)
	case opQuery:
		rc, qerr := m.QueryReachable(o.t, o.id, o.cell)
		return fmt.Sprintf("rc=%d err=%d", rc, Code(qerr))
	case opRecovery:
		ri, rerr := m.QueryNextRecovery(o.t, o.id, o.cell)
		return fmt.Sprintf("reach=%v norecover=%v next=%d reason=%d err=%d",
			ri.Reachable, ri.NoRecovery, ri.Next, ri.Reason, Code(rerr))
	}
	return fmt.Sprintf("err=%d", Code(err))
}

// genOps builds a random but self-consistent operation stream: time is
// non-decreasing with occasional deliberate rollbacks, ids are recycled from
// tracked pools, and cells are sometimes outside the footprint.
func genOps(rng *rand.Rand, n int) []diffOp {
	var ops []diffOp
	t := int64(1)
	merchants := []string{}
	merchantRegions := map[string]string{}
	merchantRings := map[string]int{}
	events := map[string]bool{} // true if still live (not terminated)
	type orec struct {
		id, m string
		cell  Cell
	}
	var orders []orec
	orderState := map[string]string{}
	eventSeq, orderSeq := 0, 0
	for i := 0; i < n; i++ {
		if rng.Intn(6) == 0 {
			t += int64(rng.Intn(3)) // sometimes time skips
		}
		op := diffOp{t: t}
		kind := opKind(rng.Intn(10))
		if len(merchants) == 0 {
			kind = opRegister
		}
		switch kind {
		case opRegister:
			id := fmt.Sprintf("m%d", len(merchants))
			maxRing := rng.Intn(6)
			region := fmt.Sprintf("r%d", rng.Intn(3))
			op.kind, op.id, op.region, op.level = opRegister, id, region, maxRing
			if !contains(merchants, id) {
				merchants = append(merchants, id)
				merchantRegions[id] = region
				merchantRings[id] = maxRing
			}
		case opMerchantLevel:
			id := merchants[rng.Intn(len(merchants))]
			op.kind, op.id, op.level = opMerchantLevel, id, rng.Intn(8)
		case opAddEvent:
			eventSeq++
			id := fmt.Sprintf("e%d", eventSeq)
			m := merchants[rng.Intn(len(merchants))]
			region := merchantRegions[m]
			start := t - int64(rng.Intn(5))
			if start < 0 {
				start = 0
			}
			op.kind, op.id, op.region = opAddEvent, id, region
			op.start, op.end = start, start+1+int64(rng.Intn(12))
			op.level = rng.Intn(7)
			events[id] = true
		case opTerminate:
			var live []string
			for id, alive := range events {
				if alive {
					live = append(live, id)
				}
			}
			op.kind = opTerminate
			if len(live) > 0 && rng.Intn(2) == 0 {
				op.id = live[rng.Intn(len(live))]
				op.at = t - int64(rng.Intn(3))
				events[op.id] = false
			} else {
				op.id = fmt.Sprintf("ghost%d", rng.Intn(100))
				op.at = t
			}
		case opPlace:
			m := merchants[rng.Intn(len(merchants))]
			maxRing := merchantRings[m]
			var cell Cell
			if rng.Intn(5) == 0 {
				cell = Cell{X: int64(maxRing + 1 + rng.Intn(3)), Y: 1} // outside forever
			} else {
				cell = Cell{X: int64(rng.Intn(maxRing + 1))}
			}
			orderSeq++
			id := fmt.Sprintf("o%d", orderSeq)
			op.kind, op.id, op.region, op.cell = opPlace, id, m, cell
			orders = append(orders, orec{id: id, m: m, cell: cell})
			orderState[id] = "pending"
		case opAccept:
			op.kind = opAccept
			if rng.Intn(5) == 0 {
				op.id = "ghost-order"
			} else if len(orders) > 0 {
				op.id = orders[rng.Intn(len(orders))].id
			} else {
				op.id = fmt.Sprintf("o%d", rng.Intn(100))
			}
		case opReroute:
			op.kind = opReroute
			if len(orders) > 0 {
				rec := orders[rng.Intn(len(orders))]
				op.id = rec.id
				maxRing := merchantRings[rec.m]
				if rng.Intn(5) == 0 {
					op.cell = Cell{X: int64(maxRing + 1), Y: 1}
				} else {
					op.cell = Cell{X: int64(rng.Intn(maxRing + 1))}
				}
			} else {
				op.id = "ghost-order"
			}
		case opDeliver:
			op.kind = opDeliver
			if len(orders) > 0 {
				op.id = orders[rng.Intn(len(orders))].id
			} else {
				op.id = "ghost-order"
			}
		case opQuery, opRecovery:
			m := merchants[rng.Intn(len(merchants))]
			maxRing := merchantRings[m]
			var cell Cell
			if rng.Intn(5) == 0 {
				cell = Cell{X: int64(maxRing + 1 + rng.Intn(3)), Y: 1}
			} else {
				cell = Cell{X: int64(rng.Intn(maxRing + 1))}
			}
			op.kind = kind
			op.id, op.cell = m, cell
		}
		// Inject occasional stale timestamps (rollback attempts).
		if rng.Intn(8) == 0 && i > 2 {
			op.t = t - int64(1+rng.Intn(3))
		}
		ops = append(ops, op)
		_ = orderState
		if op.t > t {
			t = op.t
		}
	}
	return ops
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// reasonText explains the judgment basis used for a result code.
func reasonText(result string) string {
	switch {
	case strings.Contains(result, "err=0") && strings.Contains(result, "rc=2"):
		return "ring in footprint but > effective radius -> temporary shrink"
	case strings.Contains(result, "err=0") && strings.Contains(result, "rc=1"):
		return "ring absent from footprint -> permanently outside"
	case strings.Contains(result, "err=0") && strings.Contains(result, "rc=0"):
		return "ring <= base radius - effective level -> reachable"
	case strings.Contains(result, "err=12"):
		return "permanently outside base range"
	case strings.Contains(result, "err=11"):
		return "temporarily unreachable (shrink)"
	case strings.Contains(result, "err=1"):
		return "clock rollback rejected, no state change"
	case strings.Contains(result, "err=0"):
		return "accepted"
	default:
		return "rejected with typed error code"
	}
}

func TestDifferentialRandom(t *testing.T) {
	for seed := int64(1); seed <= 120; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			ops := genOps(rng, 300)
			indexed := NewSystem()
			naive := NewNaiveSystem()
			var logb strings.Builder
			for i, op := range ops {
				got := applyOp(indexed, op)
				want := applyOp(naive, op)
				fmt.Fprintf(&logb, "[%03d] IN  %s\n", i, op)
				fmt.Fprintf(&logb, "      OUT indexed: %s | naive: %s\n", got, want)
				fmt.Fprintf(&logb, "      WHY %s\n", reasonText(got))
				if got != want {
					t.Fatalf("seed=%d step=%d divergence\nop=%s\nindexed=%s\nnaive=%s\n\nlog:\n%s",
						seed, i, op, got, want, logb.String())
				}
			}
			// Cross-check full order-state equivalence at the end.
			ids := make([]string, 0)
			for i, op := range ops {
				if op.kind == opPlace {
					ids = append(ids, op.id)
				}
				_ = i
			}
			sort.Strings(ids)
			for _, id := range ids {
				a, errA := indexed.GetOrder(id)
				b, errB := naive.GetOrder(id)
				if Code(errA) != Code(errB) {
					t.Fatalf("seed=%d order %s lookup: %v vs %v", seed, id, errA, errB)
				}
				if errA == nil && a != b {
					t.Fatalf("seed=%d order %s mismatch:\nindexed=%+v\nnaive=%+v", seed, id, a, b)
				}
			}
			if testing.Verbose() {
				t.Logf("\n%s", logb.String())
			}
		})
	}
}
