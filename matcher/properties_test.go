package matcher_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/matcher"
)

// Concurrent mixed operations must not race and must leave every surviving
// order quantity-conserving. Determinism of a fixed serial replay is covered
// by the differential tests; here we only validate atomicity/safety.
func TestConcurrentOperationsAreSafe(t *testing.T) {
	e := matcher.NewEngine()
	for i := 0; i < 200; i++ {
		_, err := e.Submit(matcher.NewOrderRequest{
			ID: fmt.Sprintf("p%d", i),
			Side: func() matcher.Side {
				if i%2 == 0 {
					return matcher.Sell
				}
				return matcher.Buy
			}(),
			Price:    int64(1 + (i % 5)),
			Quantity: int64(1 + i%7),
			Kind: func() matcher.OrderKind {
				switch i % 3 {
				case 1:
					return matcher.Iceberg
				case 2:
					return matcher.Hidden
				default:
					return matcher.Plain
				}
			}(),
			DisplaySize: int64(1 + i%4),
		})
		_ = err
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				id := fmt.Sprintf("p%d", (base+i)%200)
				switch i % 4 {
				case 0:
					_, _ = e.Submit(matcher.NewOrderRequest{
						ID: fmt.Sprintf("c%d-%d", base, i), Side: matcher.Side(i % 2),
						Price: int64(1 + i%5), Quantity: int64(1 + i%6), Kind: matcher.Plain,
					})
				case 1:
					_, _ = e.Cancel(id)
				case 2:
					_, _ = e.Modify(id, int64(1+i%9))
				case 3:
					_, _ = e.BestBid()
					_, _ = e.GetOrder(id)
					_ = e.Trades()
				}
			}
		}(w * 37)
	}
	wg.Wait()

	for i := 0; i < 200; i++ {
		o, ok := e.GetOrder(fmt.Sprintf("p%d", i))
		if ok && o.Filled+o.Remaining != o.Total {
			t.Fatalf("conservation broken p%d: %+v", i, o)
		}
	}
}

// Complexity proof: with many resting orders spread over many prices, an
// aggressor that only touches b batches across l levels must charge work
// proportional to b and l, independent of total book size. We verify this by
// comparing internal counters against the actually touched batches/levels
// and by showing the counters do not grow when the unrelated book grows.
func TestWorkScalesWithTouchedBatchesAndLevels(t *testing.T) {
	// Build a large, unrelated resting book far away from the action.
	big := matcher.NewEngine()
	for i := 0; i < 2000; i++ {
		mustSubmit(t, big, matcher.NewOrderRequest{
			ID: fmt.Sprintf("z%d", i), Side: matcher.Sell,
			Price: int64(1000 + i), Quantity: int64(1 + i%50), Kind: matcher.Plain,
		})
	}

	// Small action engine: 3 levels, each with a known number of visible
	// batches; an aggressor sweeps exactly the first two levels.
	small := matcher.NewEngine()
	// level 10: two batches (plain + one iceberg display)
	mustSubmit(t, small, matcher.NewOrderRequest{ID: "a", Side: matcher.Sell, Price: 10, Quantity: 2, Kind: matcher.Plain})
	mustSubmit(t, small, matcher.NewOrderRequest{ID: "ice", Side: matcher.Sell, Price: 11, Quantity: 4, Kind: matcher.Iceberg, DisplaySize: 2})
	mustSubmit(t, small, matcher.NewOrderRequest{ID: "c", Side: matcher.Sell, Price: 12, Quantity: 5, Kind: matcher.Plain})

	before := small.Stats()
	res := mustSubmit(t, small, matcher.NewOrderRequest{
		ID: "sweep", Side: matcher.Buy, Price: 11, Quantity: 6, Kind: matcher.Plain,
	})
	after := small.Stats()

	// Expect trades at price 10 (a: 2) and price 11 (ice: 2 batches of 2).
	if len(res.Trades) != 3 {
		t.Fatalf("trades=%+v", res.Trades)
	}
	batches := after.BatchesConsumed - before.BatchesConsumed
	levels := after.LevelsCrossed - before.LevelsCrossed
	if batches != 3 {
		t.Fatalf("batches touched = %d, want 3", batches)
	}
	if levels != 2 {
		t.Fatalf("levels crossed = %d, want 2", levels)
	}

	// The big engine performs the identical sweep at a fresh tiny set of
	// crossing prices after its 2000 unrelated orders are resting. Its
	// counters for the sweep must match the small engine's despite the book
	// being ~1000x larger.
	mustSubmit(t, big, matcher.NewOrderRequest{ID: "a", Side: matcher.Sell, Price: 10, Quantity: 2, Kind: matcher.Plain})
	mustSubmit(t, big, matcher.NewOrderRequest{ID: "ice", Side: matcher.Sell, Price: 11, Quantity: 4, Kind: matcher.Iceberg, DisplaySize: 2})
	mustSubmit(t, big, matcher.NewOrderRequest{ID: "c", Side: matcher.Sell, Price: 12, Quantity: 5, Kind: matcher.Plain})
	bbefore := big.Stats()
	bres := mustSubmit(t, big, matcher.NewOrderRequest{
		ID: "sweep", Side: matcher.Buy, Price: 11, Quantity: 6, Kind: matcher.Plain,
	})
	bafter := big.Stats()
	if len(bres.Trades) != len(res.Trades) {
		t.Fatalf("trade count differs: %d vs %d", len(bres.Trades), len(res.Trades))
	}
	if bafter.BatchesConsumed-bbefore.BatchesConsumed != 3 {
		t.Fatalf("big batches = %d, want 3", bafter.BatchesConsumed-bbefore.BatchesConsumed)
	}
	if bafter.LevelsCrossed-bbefore.LevelsCrossed != 2 {
		t.Fatalf("big levels = %d, want 2", bafter.LevelsCrossed-bbefore.LevelsCrossed)
	}
}
