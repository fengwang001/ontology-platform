package orderbook

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type op struct {
	kind   string
	order  Order
	id     int64
	newQty int64
	side   Side
	n      int
}

func (o op) String() string {
	switch o.kind {
	case "submit":
		return fmt.Sprintf("SUBMIT id=%d side=%d price=%d qty=%d",
			o.order.ID, o.order.Side, o.order.Price, o.order.Qty)
	case "cancel":
		return fmt.Sprintf("CANCEL id=%d", o.id)
	case "amend":
		return fmt.Sprintf("AMEND id=%d newQty=%d", o.id, o.newQty)
	default:
		return fmt.Sprintf("DEPTH side=%d n=%d", o.side, o.n)
	}
}

// TestAgainstNaiveRandom replays thousands of random operations on both the
// production Engine and the literal naiveEngine, asserting identical trades,
// errors and depth after every step. Inputs, outputs and the judgement rule
// are logged (go test -v).
func TestAgainstNaiveRandom(t *testing.T) {
	for _, tick := range []int64{1, 5, 100} {
		tick := tick
		t.Run(fmt.Sprintf("tick=%d", tick), func(t *testing.T) {
			rng := rand.New(rand.NewSource(20261001 + tick))
			eng, err := NewEngine(tick)
			if err != nil {
				t.Fatal(err)
			}
			na := newNaive(tick)
			var nextID int64 = 1
			newID := func() int64 {
				id := nextID
				nextID++
				return id
			}
			anyID := func() int64 { return rng.Int63n(45) + 1 }

			for step := 0; step < 3000; step++ {
				var cur op
				switch r := rng.Float64(); {
				case r < 0.6:
					id := newID()
					if rng.Intn(8) == 0 {
						id = anyID()
					}
					side := Buy
					if rng.Intn(2) == 1 {
						side = Sell
					}
					if rng.Intn(30) == 0 {
						side = Side(0)
					}
					price := (rng.Int63n(20) + 1) * tick
					switch rng.Intn(25) {
					case 0:
						price = 0
					case 1:
						price = MaxValue + 1
					case 2:
						price = tick + 1
					case 3:
						price = -tick
					}
					qty := rng.Int63n(10) + 1
					if rng.Intn(20) == 0 {
						qty = 0
					}
					if rng.Intn(80) == 0 {
						qty = MaxValue + 1
					}
					cur = op{kind: "submit", order: Order{ID: id, Side: side, Price: price, Qty: qty}}
				case r < 0.8:
					qty := rng.Int63n(12) + 1
					if rng.Intn(15) == 0 {
						qty = 0
					}
					cur = op{kind: "amend", id: anyID(), newQty: qty}
				case r < 0.92:
					cur = op{kind: "cancel", id: anyID()}
				default:
					side := Buy
					if rng.Intn(2) == 1 {
						side = Sell
					}
					cur = op{kind: "depth", side: side, n: rng.Intn(5) + 1}
				}

				t.Logf("INPUT  step=%d %s", step, cur)
				switch cur.kind {
				case "submit":
					got, gerr := eng.Submit(cur.order)
					want, werr := na.submit(cur.order)
					if !sameErr(gerr, werr) {
						t.Fatalf("step %d %v: error mismatch got=%v want=%v", step, cur, gerr, werr)
					}
					if gerr == nil && !reflect.DeepEqual(got, want) {
						t.Fatalf("step %d %v: trades mismatch\n got=%v\nwant=%v", step, cur, got, want)
					}
					t.Logf("OUTPUT trades=%v err=%v | judgement: reject id>side>price>qty; else best opposite level first, passive price, seq order; remainder rests at level tail", got, gerr)
				case "cancel":
					got, gerr := eng.Cancel(cur.id)
					want, werr := na.cancel(cur.id)
					if !sameErr(gerr, werr) || got != want {
						t.Fatalf("step %d %v: got=(%d,%v) want=(%d,%v)", step, cur, got, gerr, want, werr)
					}
					t.Logf("OUTPUT removed=%d err=%v | judgement: unknown id / already filled / already cancelled are distinct", got, gerr)
				case "amend":
					gerr := eng.Amend(cur.id, cur.newQty)
					werr := na.amend(cur.id, cur.newQty)
					if !sameErr(gerr, werr) {
						t.Fatalf("step %d %v: got=%v want=%v", step, cur, gerr, werr)
					}
					t.Logf("OUTPUT err=%v | judgement: decrease keeps queue position, equal is no-op, increase moves to level tail with new seq", gerr)
				default:
					got, gerr := eng.Depth(cur.side, cur.n)
					want := na.depth(cur.side, cur.n)
					if !sameErr(gerr, nil) || !reflect.DeepEqual(got, want) {
						t.Fatalf("step %d %v: got=(%v,%v) want=%v", step, cur, got, gerr, want)
					}
					t.Logf("OUTPUT depth=%v | judgement: best-first levels with qty>0 only, top n", got)
				}
				if step%100 == 0 {
					na.checkInvariant(t, fmt.Sprintf("step %d", step))
				}
			}
			na.checkInvariant(t, "end")
		})
	}
}
