package orderbook

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustSubmit(t *testing.T, e *Engine, id int64, side Side, price, qty int64) []Trade {
	t.Helper()
	tr, err := e.Submit(Order{ID: id, Side: side, Price: price, Qty: qty})
	if err != nil {
		t.Fatalf("submit id=%d: %v", id, err)
	}
	return tr
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

// Equal buy and sell prices cross immediately.
func TestEqualPriceCrosses(t *testing.T) {
	e, _ := NewEngine(1)
	mustSubmit(t, e, 1, Sell, 100, 5)
	tr := mustSubmit(t, e, 2, Buy, 100, 5)
	want := []Trade{{PassiveID: 1, AggressorID: 2, Price: 100, Qty: 5}}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("trades = %v, want %v", tr, want)
	}
	if d, _ := e.Depth(Buy, 10); len(d) != 0 {
		t.Fatalf("bids should be empty, got %v", d)
	}
	if d, _ := e.Depth(Sell, 10); len(d) != 0 {
		t.Fatalf("asks should be empty, got %v", d)
	}
}

// Trade price is always the passive (resting) order's price, both sides.
func TestPriceIsPassivePrice(t *testing.T) {
	e, _ := NewEngine(1)
	mustSubmit(t, e, 1, Sell, 100, 4)
	tr := mustSubmit(t, e, 2, Buy, 105, 4)
	if len(tr) != 1 || tr[0].Price != 100 {
		t.Fatalf("aggressive buy priced 105 must trade at passive 100, got %v", tr)
	}

	mustSubmit(t, e, 3, Buy, 90, 3)
	tr = mustSubmit(t, e, 4, Sell, 85, 3)
	if len(tr) != 1 || tr[0].Price != 90 {
		t.Fatalf("aggressive sell priced 85 must trade at passive 90, got %v", tr)
	}
}

// At one price, fills follow arrival order; quantity is min of the rests.
func TestFIFOAtSamePrice(t *testing.T) {
	e, _ := NewEngine(1)
	mustSubmit(t, e, 1, Sell, 100, 2)
	mustSubmit(t, e, 2, Sell, 100, 9)
	mustSubmit(t, e, 3, Sell, 100, 4)
	tr := mustSubmit(t, e, 4, Buy, 100, 8)
	want := []Trade{
		{1, 4, 100, 2},
		{2, 4, 100, 6},
	}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("trades = %v, want %v", tr, want)
	}
	d, _ := e.Depth(Sell, 1)
	if !reflect.DeepEqual(d, []Level{{Price: 100, Qty: 7, Count: 2}}) {
		t.Fatalf("depth = %v", d)
	}
}

// One aggressive order sweeps several price levels and rests the remainder
// at its own price.
func TestSweepMultipleLevelsAndRest(t *testing.T) {
	e, _ := NewEngine(1)
	mustSubmit(t, e, 1, Sell, 100, 2)
	mustSubmit(t, e, 2, Sell, 101, 3)
	mustSubmit(t, e, 3, Sell, 103, 10)
	tr := mustSubmit(t, e, 9, Buy, 102, 10)
	want := []Trade{
		{1, 9, 100, 2},
		{2, 9, 101, 3},
	}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("trades = %v, want %v", tr, want)
	}
	// Level 103 is not crossed (103 > 102); 5 units rest at 102.
	d, _ := e.Depth(Buy, 5)
	if !reflect.DeepEqual(d, []Level{{Price: 102, Qty: 5, Count: 1}}) {
		t.Fatalf("bids = %v", d)
	}
	d, _ = e.Depth(Sell, 5)
	if !reflect.DeepEqual(d, []Level{{Price: 103, Qty: 10, Count: 1}}) {
		t.Fatalf("asks = %v", d)
	}
	// A later sell crossing 102 trades the resting remainder at price 102.
	tr = mustSubmit(t, e, 10, Sell, 102, 5)
	if !reflect.DeepEqual(tr, []Trade{{9, 10, 102, 5}}) {
		t.Fatalf("trades = %v", tr)
	}
}

// Amend down keeps queue position; amend up moves to the level tail;
// amend equal is a successful no-op that keeps position.
func TestAmendQueuePosition(t *testing.T) {
	setup := func(t *testing.T) *Engine {
		e, _ := NewEngine(1)
		mustSubmit(t, e, 1, Sell, 100, 5)
		mustSubmit(t, e, 2, Sell, 100, 5)
		mustSubmit(t, e, 3, Sell, 100, 5)
		return e
	}

	t.Run("decrease keeps position", func(t *testing.T) {
		e := setup(t)
		if err := e.Amend(2, 3); err != nil {
			t.Fatal(err)
		}
		tr := mustSubmit(t, e, 4, Buy, 100, 8)
		want := []Trade{{1, 4, 100, 5}, {2, 4, 100, 3}}
		if !reflect.DeepEqual(tr, want) {
			t.Fatalf("trades = %v, want %v", tr, want)
		}
		if d, _ := e.Depth(Sell, 1); !reflect.DeepEqual(d, []Level{{100, 5, 1}}) {
			t.Fatalf("depth = %v, only C(5) should remain", d)
		}
	})

	t.Run("increase loses position", func(t *testing.T) {
		e := setup(t)
		if err := e.Amend(2, 8); err != nil {
			t.Fatal(err)
		}
		tr := mustSubmit(t, e, 4, Buy, 100, 8)
		// Queue is now A, C, B: A(5), then C(3 of 5), B untouched.
		want := []Trade{{1, 4, 100, 5}, {3, 4, 100, 3}}
		if !reflect.DeepEqual(tr, want) {
			t.Fatalf("trades = %v, want %v", tr, want)
		}
		if d, _ := e.Depth(Sell, 1); !reflect.DeepEqual(d, []Level{{100, 10, 2}}) {
			t.Fatalf("depth = %v, want C rem 2 + B 8 = 10 over 2 orders", d)
		}
	})

	t.Run("equal is no-op keeping position", func(t *testing.T) {
		e := setup(t)
		if err := e.Amend(2, 5); err != nil {
			t.Fatal(err)
		}
		tr := mustSubmit(t, e, 4, Buy, 100, 6)
		want := []Trade{{1, 4, 100, 5}, {2, 4, 100, 1}}
		if !reflect.DeepEqual(tr, want) {
			t.Fatalf("trades = %v, want %v", tr, want)
		}
	})
}

// Cancel returns removed quantity; the three failure reasons are distinct.
func TestCancelSemantics(t *testing.T) {
	e, _ := NewEngine(1)
	mustSubmit(t, e, 1, Sell, 100, 7)
	mustSubmit(t, e, 2, Buy, 90, 4)

	got, err := e.Cancel(1)
	if err != nil || got != 7 {
		t.Fatalf("cancel live = (%d,%v), want 7,nil", got, err)
	}

	tr := mustSubmit(t, e, 3, Sell, 90, 4) // fills order 2
	if len(tr) != 1 || tr[0].Qty != 4 {
		t.Fatalf("tr = %v", tr)
	}
	_, err = e.Cancel(2)
	wantErr(t, err, ErrAlreadyFilled)

	_, err = e.Cancel(1)
	wantErr(t, err, ErrAlreadyCancelled)

	_, err = e.Cancel(999)
	wantErr(t, err, ErrUnknownID)

	if d, _ := e.Depth(Sell, 10); len(d) != 0 {
		t.Fatalf("book should be empty, got %v", d)
	}
}

// An id is never reusable whether it is live, filled or cancelled.
func TestIDReuseRejected(t *testing.T) {
	e, _ := NewEngine(1)
	mustSubmit(t, e, 1, Buy, 90, 2)
	_, err := e.Submit(Order{ID: 1, Side: Sell, Price: 90, Qty: 2})
	wantErr(t, err, ErrIDReused)

	mustSubmit(t, e, 2, Sell, 90, 2) // crosses with id 1; both fill
	_, err = e.Submit(Order{ID: 1, Side: Buy, Price: 90, Qty: 1})
	wantErr(t, err, ErrIDReused)
	_, err = e.Submit(Order{ID: 2, Side: Buy, Price: 90, Qty: 1})
	wantErr(t, err, ErrIDReused)

	mustSubmit(t, e, 3, Buy, 80, 1)
	if _, err := e.Cancel(3); err != nil {
		t.Fatal(err)
	}
	_, err = e.Submit(Order{ID: 3, Side: Buy, Price: 80, Qty: 1})
	wantErr(t, err, ErrIDReused)
}

// Rejection order for Submit: id reuse, side, price, qty. Amend: id
// existence, in-book state, then newQty. Only the first reason is reported.
func TestRejectionOrder(t *testing.T) {
	e, _ := NewEngine(5)
	mustSubmit(t, e, 1, Buy, 100, 3)

	_, err := e.Submit(Order{ID: 1, Side: Side(0), Price: 0, Qty: 0})
	wantErr(t, err, ErrIDReused)

	_, err = e.Submit(Order{ID: 2, Side: Side(7), Price: 0, Qty: 0})
	wantErr(t, err, ErrInvalidSide)

	_, err = e.Submit(Order{ID: 3, Side: Buy, Price: 103, Qty: 0})
	wantErr(t, err, ErrInvalidPrice)
	_, err = e.Submit(Order{ID: 4, Side: Buy, Price: 0, Qty: 0})
	wantErr(t, err, ErrInvalidPrice)
	_, err = e.Submit(Order{ID: 5, Side: Buy, Price: MaxValue + 5, Qty: 1})
	wantErr(t, err, ErrInvalidPrice)

	_, err = e.Submit(Order{ID: 6, Side: Buy, Price: 100, Qty: MaxValue + 1})
	wantErr(t, err, ErrInvalidQty)

	wantErr(t, e.Amend(999, 0), ErrUnknownID)
	mustSubmit(t, e, 7, Sell, 100, 3) // fills order 1
	wantErr(t, e.Amend(1, 0), ErrAlreadyFilled)
	mustSubmit(t, e, 8, Sell, 110, 4)
	wantErr(t, e.Amend(8, 0), ErrInvalidQty)
	wantErr(t, e.Amend(8, -3), ErrInvalidQty)
	wantErr(t, e.Amend(8, MaxValue+1), ErrInvalidQty)

	for _, bad := range []int64{0, -1, -10} {
		if _, err := NewEngine(bad); !errors.Is(err, ErrTickNonPositive) {
			t.Fatalf("NewEngine(%d) err = %v", bad, err)
		}
	}
}

// A rejected operation changes neither the book nor the arrival sequence.
func TestRejectedOpsChangeNothing(t *testing.T) {
	e, _ := NewEngine(1)
	mustSubmit(t, e, 1, Sell, 100, 5)
	mustSubmit(t, e, 2, Sell, 100, 5)

	before, _ := e.Depth(Sell, 10)
	_, err := e.Submit(Order{ID: 2, Side: Buy, Price: 100, Qty: 1})
	wantErr(t, err, ErrIDReused)
	_, err = e.Submit(Order{ID: 3, Side: Buy, Price: 101, Qty: 0})
	wantErr(t, err, ErrInvalidQty)
	_, err = e.Cancel(42)
	wantErr(t, err, ErrUnknownID)
	wantErr(t, e.Amend(42, 9), ErrUnknownID)
	wantErr(t, e.Amend(2, 0), ErrInvalidQty)

	after, _ := e.Depth(Sell, 10)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("book changed after rejected ops:\nbefore=%v\nafter =%v", before, after)
	}

	// If the rejected submit had consumed a seq, order 4 would still be
	// behind orders 1 and 2; the observable proof: a fresh order rests at
	// the tail and fills after 1 and 2, and rejected amends didn't move #2.
	tr := mustSubmit(t, e, 4, Buy, 100, 7)
	want := []Trade{{1, 4, 100, 5}, {2, 4, 100, 2}}
	if !reflect.DeepEqual(tr, want) {
		t.Fatalf("trades = %v, want %v (queue altered by rejected ops?)", tr, want)
	}
}

// Boundary values and tick multiples are accepted; non-multiples rejected.
func TestTickAndBounds(t *testing.T) {
	e, _ := NewEngine(10)
	mustSubmit(t, e, 1, Buy, MaxValue-(MaxValue%10), MaxValue)
	d, _ := e.Depth(Buy, 1)
	if d[0].Price != MaxValue-(MaxValue%10) || d[0].Qty != MaxValue || d[0].Count != 1 {
		t.Fatalf("depth = %v", d)
	}
	_, err := e.Submit(Order{ID: 2, Side: Buy, Price: 15, Qty: 1})
	wantErr(t, err, ErrInvalidPrice)

	if _, err := e.Depth(Side(9), 1); !errors.Is(err, ErrInvalidSide) {
		t.Fatalf("Depth bad side err = %v", err)
	}
	if d, err := e.Depth(Buy, 0); err != nil || len(d) != 0 {
		t.Fatalf("Depth n=0 = (%v, %v)", d, err)
	}
	if d, _ := e.Depth(Sell, 1); len(d) != 0 {
		t.Fatalf("asks = %v, want empty", d)
	}
}

// Replaying the same operation sequence yields the identical trade sequence.
func TestReplayDeterminism(t *testing.T) {
	run := func() []Trade {
		e, _ := NewEngine(2)
		all := []Trade{}
		all = append(all, mustSubmit(t, e, 1, Sell, 100, 3)...)
		all = append(all, mustSubmit(t, e, 3, Sell, 100, 2)...)
		if err := e.Amend(1, 6); err != nil {
			t.Fatal(err)
		}
		all = append(all, mustSubmit(t, e, 2, Sell, 102, 4)...)
		all = append(all, mustSubmit(t, e, 4, Buy, 102, 9)...)
		all = append(all, mustSubmit(t, e, 5, Buy, 100, 10)...)
		return all
	}
	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-reproducible trades:\nfirst =%v\nsecond=%v", first, second)
	}
	want := []Trade{
		{3, 4, 100, 2}, // amend moved order 1 behind the later-arriving order 3
		{1, 4, 100, 6},
		{2, 4, 102, 1},
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("trades = %v\nwant    = %v", first, want)
	}
}

// Concurrent operations must not race and must leave a consistent book.
func TestConcurrent(t *testing.T) {
	e, _ := NewEngine(1)
	mustSubmit(t, e, 1, Buy, 90, 1000)
	mustSubmit(t, e, 2, Sell, 110, 1000)

	var wg sync.WaitGroup
	var id int64 = 100
	var mu sync.Mutex
	nextID := func() int64 {
		mu.Lock()
		defer mu.Unlock()
		id++
		return id
	}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				switch k % 4 {
				case 0:
					_, _ = e.Submit(Order{ID: nextID(), Side: Buy, Price: 95, Qty: 1})
				case 1:
					_, _ = e.Submit(Order{ID: nextID(), Side: Sell, Price: 105, Qty: 1})
				case 2:
					_, _ = e.Depth(Buy, 5)
					_, _ = e.Depth(Sell, 5)
				case 3:
					_ = e.Amend(1, 1000)
				}
			}
		}()
	}
	wg.Wait()

	bids, _ := e.Depth(Buy, 100)
	asks, _ := e.Depth(Sell, 100)
	if len(bids) > 0 && len(asks) > 0 && bids[0].Price >= asks[0].Price {
		t.Fatalf("crossed book after concurrency: bid %d ask %d", bids[0].Price, asks[0].Price)
	}
}
