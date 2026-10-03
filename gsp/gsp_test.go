package gsp

import (
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
)

func mustRegister(t *testing.T, engine *Engine, id string, bid, quality, budget int64) {
	t.Helper()
	if err := engine.Register(id, bid, quality, budget); err != nil {
		t.Fatalf("Register(%q, %d, %d, %d): %v", id, bid, quality, budget, err)
	}
}

func winnerIDs(result AuctionResult) []string {
	ids := make([]string, len(result.Winners))
	for i := range result.Winners {
		ids[i] = result.Winners[i].ID
	}
	return ids
}

func TestEligibilityBoundaries(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "equalP", 10, 1, 100)
	mustRegister(t, engine, "belowP", 9, 1, 100)

	result, err := engine.Auction(2, 10)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}
	if got, want := winnerIDs(result), []string{"equalP"}; !slices.Equal(got, want) {
		t.Fatalf("winners = %v, want %v", got, want)
	}

	engine = NewEngine()
	mustRegister(t, engine, "equalBudget", 10, 1, 10)
	mustRegister(t, engine, "shortBudget", 10, 1, 9)
	result, err = engine.Auction(2, 1)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}
	if got, want := winnerIDs(result), []string{"equalBudget"}; !slices.Equal(got, want) {
		t.Fatalf("winners = %v, want %v", got, want)
	}
}

func TestTieUsesSerial(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "later", 20, 5, 1000)
	mustRegister(t, engine, "earlier", 10, 10, 1000)

	result, err := engine.Auction(1, 1)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}
	if got, want := winnerIDs(result), []string{"later"}; !slices.Equal(got, want) {
		t.Fatalf("winners = %v, want %v", got, want)
	}
}

func TestPricingBoundaries(t *testing.T) {
	t.Run("bid cap", func(t *testing.T) {
		engine := NewEngine()
		mustRegister(t, engine, "winner", 50, 20, 1000)
		mustRegister(t, engine, "next", 20, 50, 1000)

		result, err := engine.Auction(1, 1)
		if err != nil {
			t.Fatalf("Auction: %v", err)
		}
		if result.Winners[0].Price != 50 {
			t.Fatalf("price = %d, want 50", result.Winners[0].Price)
		}
	})

	t.Run("reserve floor", func(t *testing.T) {
		engine := NewEngine()
		mustRegister(t, engine, "winner", 100, 100, 1000)
		mustRegister(t, engine, "next", 1, 1, 1000)

		result, err := engine.Auction(1, 50)
		if err != nil {
			t.Fatalf("Auction: %v", err)
		}
		if result.Winners[0].Price != 50 {
			t.Fatalf("price = %d, want 50", result.Winners[0].Price)
		}
	})

	t.Run("division adds one", func(t *testing.T) {
		engine := NewEngine()
		mustRegister(t, engine, "winner", 100, 10, 1000)
		mustRegister(t, engine, "next", 20, 10, 1000)

		result, err := engine.Auction(1, 1)
		if err != nil {
			t.Fatalf("Auction: %v", err)
		}
		if result.Winners[0].Price != 21 {
			t.Fatalf("price = %d, want 21", result.Winners[0].Price)
		}
	})

	t.Run("last eligible sets last winner price", func(t *testing.T) {
		engine := NewEngine()
		mustRegister(t, engine, "first", 100, 10, 1000)
		mustRegister(t, engine, "winnerK", 50, 10, 1000)
		mustRegister(t, engine, "afterK", 20, 10, 1000)

		result, err := engine.Auction(2, 11)
		if err != nil {
			t.Fatalf("Auction: %v", err)
		}
		if got := result.Winners; len(got) != 2 || got[0].Price != 51 || got[1].Price != 21 {
			t.Fatalf("winners = %+v, want first=51 second=21", got)
		}

		engine = NewEngine()
		mustRegister(t, engine, "only", 100, 10, 1000)
		result, err = engine.Auction(1, 12)
		if err != nil {
			t.Fatalf("Auction: %v", err)
		}
		if result.Winners[0].Price != 12 {
			t.Fatalf("price = %d, want reserve 12", result.Winners[0].Price)
		}
	})
}

func TestHeldBudgetAccumulationAndExit(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "strong", 10, 100, 15)
	mustRegister(t, engine, "weak", 7, 100, 100)

	first, err := engine.Auction(2, 1)
	if err != nil {
		t.Fatalf("first Auction: %v", err)
	}
	if got := first.Winners; len(got) != 2 || got[0].ID != "strong" || got[0].Price != 8 || got[1].Price != 1 {
		t.Fatalf("first winners = %+v", got)
	}
	if state := engine.BidderStates()[0]; state.Held != 8 || state.Budget != 15 {
		t.Fatalf("strong state = %+v, want held 8 and unchanged budget", state)
	}

	second, err := engine.Auction(2, 1)
	if err != nil {
		t.Fatalf("second Auction: %v", err)
	}
	if got, want := winnerIDs(second), []string{"weak"}; !slices.Equal(got, want) {
		t.Fatalf("second winners = %v, want %v", got, want)
	}
}

func TestEffectiveQualityDiscount(t *testing.T) {
	tests := []struct {
		name          string
		quality       int64
		missedClicks  int64
		wantEffective int64
	}{
		{name: "f equals 2", quality: 100, missedClicks: 2, wantEffective: 100},
		{name: "f equals 3", quality: 100, missedClicks: 3, wantEffective: 90},
		{name: "f equals 7 cap", quality: 100, missedClicks: 7, wantEffective: 50},
		{name: "f beyond 7 cap", quality: 100, missedClicks: 100, wantEffective: 50},
		{name: "quality floor", quality: 1, missedClicks: 7, wantEffective: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveQuality(tt.quality, tt.missedClicks); got != tt.wantEffective {
				t.Fatalf("effectiveQuality(%d, %d) = %d, want %d", tt.quality, tt.missedClicks, got, tt.wantEffective)
			}
		})
	}
}

func TestResolveClicksAndQuality(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "clicked", 40, 30, 1000)
	mustRegister(t, engine, "ignored", 50, 20, 1000)
	mustRegister(t, engine, "max", 10, 1000, 1000)
	mustRegister(t, engine, "minimum", 20, 1, 1000)

	result, err := engine.Auction(4, 10)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}
	if err := engine.Resolve(result.ID, []string{"clicked", "max"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	states := make(map[string]BidderState)
	for _, state := range engine.BidderStates() {
		states[state.ID] = state
	}
	clicked := states["clicked"]
	if clicked.Budget != 1000-34 || clicked.Held != 0 || clicked.ChargedTotal != 34 || clicked.Quality != 151 || clicked.MissedClicks != 0 {
		t.Fatalf("clicked state = %+v", clicked)
	}
	ignored := states["ignored"]
	if ignored.Budget != 1000 || ignored.Held != 0 || ignored.ChargedTotal != 0 || ignored.Quality != 18 || ignored.MissedClicks != 1 {
		t.Fatalf("ignored state = %+v", ignored)
	}
	if states["max"].Quality != 1000 || states["max"].Budget != 990 || states["max"].ChargedTotal != 10 {
		t.Fatalf("max state = %+v, want clicked price 10 and quality 1000", states["max"])
	}
	if states["minimum"].Quality != 1 || states["minimum"].MissedClicks != 1 {
		t.Fatalf("minimum state = %+v, want q=1 f=1", states["minimum"])
	}
}

func TestEmptyAndAllClicks(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "a", 20, 100, 100)
	mustRegister(t, engine, "b", 10, 100, 100)

	empty, err := engine.Auction(2, 1)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}
	if err := engine.Resolve(empty.ID, nil); err != nil {
		t.Fatalf("Resolve empty: %v", err)
	}
	for _, state := range engine.BidderStates() {
		if state.Held != 0 || state.MissedClicks != 1 || state.Budget != 100 {
			t.Fatalf("state after empty clicks = %+v", state)
		}
	}

	all, err := engine.Auction(2, 1)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}
	if err := engine.Resolve(all.ID, []string{"b", "a"}); err != nil {
		t.Fatalf("Resolve all: %v", err)
	}
	for _, state := range engine.BidderStates() {
		if state.Held != 0 || state.MissedClicks != 0 || state.ChargedTotal == 0 {
			t.Fatalf("state after all clicks = %+v", state)
		}
	}
}

func TestClickResetsMissedClicks(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "a", 20, 100, 1000)
	mustRegister(t, engine, "b", 1, 100, 1000)

	first, err := engine.Auction(1, 1)
	if err != nil {
		t.Fatalf("first Auction: %v", err)
	}
	if err := engine.Resolve(first.ID, nil); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	second, err := engine.Auction(1, 1)
	if err != nil {
		t.Fatalf("second Auction: %v", err)
	}
	if err := engine.Resolve(second.ID, []string{"a"}); err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if got := engine.BidderStates()[0].MissedClicks; got != 0 {
		t.Fatalf("missed clicks = %d, want 0", got)
	}
}

func TestEffectiveQualityFrozenAtAuction(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "winner", 10, 900, 1000)
	mustRegister(t, engine, "next", 9, 100, 1000)
	winner := engine.bidders["winner"]
	winner.missedClicks = 3

	result, err := engine.Auction(1, 1)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}
	if result.Winners[0].Price != 2 {
		t.Fatalf("frozen price = %d, want 2", result.Winners[0].Price)
	}
	if err := engine.Resolve(result.ID, []string{"winner"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	auctionState := engine.AuctionStates()[0]
	if auctionState.Winners[0].Price != 2 || winner.quality != 912 || winner.missedClicks != 0 {
		t.Fatalf("after resolve auction=%+v quality=%d missed=%d", auctionState, winner.quality, winner.missedClicks)
	}
}

func TestRejectedOperationsDoNotMutate(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "a", 20, 100, 1000)
	before := engine.BidderStates()
	beforeNextAuction := engine.auctions

	if err := engine.Register("", 1, 1, 1); !errorsIs(err, ErrInvalidArgument) {
		t.Fatalf("empty id error = %v", err)
	}
	if err := engine.Register("a", 1, 1, 1); !errorsIs(err, ErrBidderExists) {
		t.Fatalf("duplicate bidder error = %v", err)
	}
	result, err := engine.Auction(0, 1)
	if !errorsIs(err, ErrInvalidArgument) || result.ID != 0 || len(result.Winners) != 0 {
		t.Fatalf("invalid auction result=%+v err=%v", result, err)
	}
	result, err = engine.Auction(1, 100)
	if !errorsIs(err, ErrNoBidders) || result.ID != 0 || len(engine.auctions) != len(beforeNextAuction) {
		t.Fatalf("no-bidder auction changed state: result=%+v err=%v", result, err)
	}
	if err := engine.Resolve(404, nil); !errorsIs(err, ErrAuctionNotFound) {
		t.Fatalf("missing auction error = %v", err)
	}

	auction, err := engine.Auction(1, 1)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}
	if err := engine.Resolve(auction.ID, []string{"a", "a"}); !errorsIs(err, ErrInvalidArgument) {
		t.Fatalf("duplicate click error = %v", err)
	}
	if err := engine.Resolve(auction.ID, []string{"outsider"}); !errorsIs(err, ErrNotWinner) {
		t.Fatalf("non-winner error = %v", err)
	}
	if err := engine.Resolve(auction.ID, nil); err != nil {
		t.Fatalf("valid Resolve: %v", err)
	}
	if err := engine.Resolve(auction.ID, nil); !errorsIs(err, ErrAuctionSettled) {
		t.Fatalf("repeat resolve error = %v", err)
	}

	after := engine.BidderStates()
	if len(after) != len(before)+0 {
		t.Fatalf("bidder count changed")
	}
}

func TestSingleSortComparisonCounter(t *testing.T) {
	for _, count := range []int{100, 10_000} {
		engine := NewEngine()
		for i := range count {
			mustRegister(t, engine, "bidder-"+itoa(i), int64(1+i%500), 1, 1_000_000_000)
		}
		result, err := engine.Auction(100, 1)
		if err != nil {
			t.Fatalf("count=%d Auction: %v", count, err)
		}
		if len(result.Winners) != 100 {
			t.Fatalf("count=%d winners=%d, want 100", count, len(result.Winners))
		}
		comparisons := engine.lastSortComparisons
		if comparisons < count-1 || comparisons > count*ceilLog2(count) {
			t.Fatalf("count=%d comparisons=%d outside single sort range [%d,%d]", count, comparisons, count-1, count*ceilLog2(count))
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "alpha", 20, 500, 1_000_000_000)
	mustRegister(t, engine, "beta", 18, 500, 1_000_000_000)
	mustRegister(t, engine, "gamma", 16, 500, 1_000_000_000)

	const goroutines = 24
	const operations = 100
	var wait sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < goroutines; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			for operation := 0; operation < operations; operation++ {
				result, err := engine.Auction(3, 1)
				if err != nil {
					t.Errorf("worker %d Auction: %v", worker, err)
					return
				}
				clicks := []string{}
				for _, winner := range result.Winners {
					if (worker+operation+int(winner.Price))%2 == 0 {
						clicks = append(clicks, winner.ID)
					}
				}
				if err := engine.Resolve(result.ID, clicks); err != nil {
					t.Errorf("worker %d Resolve(%d): %v", worker, result.ID, err)
					return
				}
			}
		}(worker)
	}
	close(start)
	wait.Wait()

	auctions := engine.AuctionStates()
	if len(auctions) != goroutines*operations {
		t.Fatalf("auctions = %d, want %d", len(auctions), goroutines*operations)
	}
	for i, auction := range auctions {
		if auction.ID != int64(i+1) || !auction.Settled {
			t.Fatalf("auction %+v has wrong sequence or settlement state", auction)
		}
		seen := make(map[string]struct{})
		for _, winner := range auction.Winners {
			if _, duplicated := seen[winner.ID]; duplicated {
				t.Fatalf("duplicate winner %s in auction %d", winner.ID, auction.ID)
			}
			seen[winner.ID] = struct{}{}
			if winner.Price < 1 || winner.Price > 20 {
				t.Fatalf("invalid price %d in auction %d", winner.Price, auction.ID)
			}
		}
	}

	for _, bidder := range engine.BidderStates() {
		if bidder.Held != 0 || bidder.Budget < 0 || bidder.Budget > 1_000_000_000 || bidder.ChargedTotal > 1_000_000_000-bidder.Budget {
			t.Fatalf("invalid concurrent final state: %+v", bidder)
		}
	}
}

func TestRejectedCallsKeepExactState(t *testing.T) {
	engine := NewEngine()
	mustRegister(t, engine, "a", 20, 100, 1000)
	auction, err := engine.Auction(1, 1)
	if err != nil {
		t.Fatalf("Auction: %v", err)
	}

	snapshotBidders := engine.BidderStates()
	snapshotAuctions := engine.AuctionStates()
	invalidCalls := []func() error{
		func() error { return engine.Register("a", 1, 1, 1) },
		func() error { return engine.Register("x", 0, 1, 1) },
		func() error { return engine.Register("x", 1_000_001, 1, 1) },
		func() error { return engine.Register("x", 1, 0, 1) },
		func() error { return engine.Register("x", 1, 1001, 1) },
		func() error { return engine.Register("x", 1, 1, 0) },
		func() error { return engine.Register("x", 1, 1, 1_000_000_000_001) },
		func() error { _, err := engine.Auction(0, 1); return err },
		func() error { _, err := engine.Auction(101, 1); return err },
		func() error { _, err := engine.Auction(1, 0); return err },
		func() error { _, err := engine.Auction(1, 1_000_001); return err },
		func() error { return engine.Resolve(auction.ID, []string{"a", "a"}) },
		func() error { return engine.Resolve(auction.ID, []string{"x"}) },
		func() error { return engine.Resolve(auction.ID+1, nil) },
	}
	for _, call := range invalidCalls {
		if err := call(); err == nil {
			t.Fatal("invalid call unexpectedly succeeded")
		}
		if got := engine.BidderStates(); !slices.EqualFunc(got, snapshotBidders, func(left, right BidderState) bool { return left == right }) {
			t.Fatalf("bidder state changed after rejected call: before=%+v after=%+v", snapshotBidders, got)
		}
		if got := engine.AuctionStates(); !slices.EqualFunc(got, snapshotAuctions, func(left, right AuctionState) bool {
			return left.ID == right.ID && left.Settled == right.Settled && slices.Equal(left.Winners, right.Winners)
		}) {
			t.Fatalf("auction state changed after rejected call: before=%+v after=%+v", snapshotAuctions, got)
		}
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var bytes [20]byte
	position := len(bytes)
	for value > 0 {
		position--
		bytes[position] = byte('0' + value%10)
		value /= 10
	}
	return string(bytes[position:])
}

func ceilLog2(value int) int {
	levels := 0
	for power := 1; power < value; power *= 2 {
		levels++
	}
	return levels
}

func errorsIs(err, target error) bool {
	return err == target
}

var _ = rand.New(rand.NewPCG(1, 2))
