package limit_test

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"testing"

	"ontology/limit"
)

func newCtrl(t *testing.T, lambda int64) *limit.Controller {
	t.Helper()
	c, err := limit.New(lambda)
	if err != nil {
		t.Fatalf("New(%d): %v", lambda, err)
	}
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func wantExposure(t *testing.T, c *limit.Controller, cp string, want int64) {
	t.Helper()
	e, err := c.Exposure([]byte(cp))
	if err != nil {
		t.Fatalf("Exposure(%s): %v", cp, err)
	}
	if e.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("Exposure(%s) = %s, want %d", cp, e, want)
	}
}

type breachWant struct {
	name string
	exp  int64
}

func wantBreaches(t *testing.T, c *limit.Controller, want ...breachWant) {
	t.Helper()
	got := c.Breaches()
	if len(got) != len(want) {
		t.Fatalf("Breaches() has %d entries, want %d: %+v", len(got), len(want), got)
	}
	seqs := make(map[uint64]bool)
	for i, b := range got {
		if string(b.Counterparty) != want[i].name || b.Exposure.Cmp(big.NewInt(want[i].exp)) != 0 {
			t.Fatalf("Breaches()[%d] = (%s, %s), want (%s, %d)",
				i, b.Counterparty, b.Exposure, want[i].name, want[i].exp)
		}
		if seqs[b.Seq] {
			t.Fatalf("duplicate breach seq %d", b.Seq)
		}
		seqs[b.Seq] = true
		if i > 0 && got[i-1].Seq >= b.Seq {
			t.Fatalf("breach seqs not ascending: %+v", got)
		}
	}
}

func TestNewInvalidLambda(t *testing.T) {
	if _, err := limit.New(-1); !errors.Is(err, limit.ErrInvalidParam) {
		t.Fatalf("New(-1) = %v, want ErrInvalidParam", err)
	}
	if _, err := limit.New(10001); !errors.Is(err, limit.ErrInvalidParam) {
		t.Fatalf("New(10001) = %v, want ErrInvalidParam", err)
	}
	if _, err := limit.New(0); err != nil {
		t.Fatalf("New(0): %v", err)
	}
	if _, err := limit.New(10000); err != nil {
		t.Fatalf("New(10000): %v", err)
	}
}

// TestExample1 replays the first scenario from the specification (lambda=0).
func TestExample1(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("C"), 100))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 20))

	must(t, c.Trade([]byte("C"), []byte("X"), 8))
	wantExposure(t, c, "C", 80)

	wantErr(t, c.Trade([]byte("C"), []byte("Y"), 2), limit.ErrLimitExceeded)
	wantExposure(t, c, "C", 80)

	must(t, c.SetPrice([]byte("X"), 15))
	wantExposure(t, c, "C", 120)
	wantBreaches(t, c, breachWant{"C", 120})

	must(t, c.Trade([]byte("C"), []byte("X"), -1))
	wantExposure(t, c, "C", 105)
	wantBreaches(t, c, breachWant{"C", 105})

	must(t, c.Post([]byte("C"), 30))
	wantExposure(t, c, "C", 75)
	wantBreaches(t, c)
}

// TestExample2 replays the second scenario from the specification
// (lambda=1000), including the per-group rounding distinction and the
// breach ordering where D (registered later, breaching earlier) keeps
// sequence number 1 ahead of C.
func TestExample2(t *testing.T) {
	c := newCtrl(t, 1000)
	must(t, c.Register([]byte("C"), 100))
	must(t, c.Register([]byte("D"), 50))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("V"), 11))
	must(t, c.SetPrice([]byte("Z1"), 7))
	must(t, c.SetPrice([]byte("Z2"), 3))
	must(t, c.SetGroup([]byte("X"), []byte("g1")))
	must(t, c.SetGroup([]byte("V"), []byte("g1")))
	must(t, c.SetGroup([]byte("Z1"), []byte("g2")))
	must(t, c.SetGroup([]byte("Z2"), []byte("g2")))

	must(t, c.Trade([]byte("C"), []byte("X"), 8))
	wantExposure(t, c, "C", 80)

	must(t, c.Trade([]byte("C"), []byte("V"), -5))
	wantExposure(t, c, "C", 31)

	must(t, c.Trade([]byte("C"), []byte("Z1"), 5))
	wantExposure(t, c, "C", 66)

	must(t, c.Trade([]byte("C"), []byte("Z2"), -4))
	// Per-group rounding: 48 + ceil(5.5) + ceil(1.2) = 48+6+2 = 56.
	// Rounding the summed hedge would give 48 + ceil(6.7) = 55.
	wantExposure(t, c, "C", 56)

	must(t, c.Post([]byte("C"), 6))
	wantExposure(t, c, "C", 50)

	must(t, c.Trade([]byte("D"), []byte("X"), 4))
	wantExposure(t, c, "D", 40)
	wantBreaches(t, c)

	must(t, c.SetPrice([]byte("X"), 13))
	wantExposure(t, c, "C", 74)
	wantExposure(t, c, "D", 52)
	wantBreaches(t, c, breachWant{"D", 52})

	must(t, c.SetPrice([]byte("X"), 20))
	wantExposure(t, c, "C", 130)
	wantExposure(t, c, "D", 80)
	wantBreaches(t, c, breachWant{"D", 80}, breachWant{"C", 130})
}

// TestExactLimitBoundary covers E' == L accepted and E' == L+1 rejected.
func TestExactLimitBoundary(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("A"), 80))
	must(t, c.Register([]byte("B"), 80))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 1))

	must(t, c.Trade([]byte("A"), []byte("X"), 8))
	wantExposure(t, c, "A", 80)

	must(t, c.Trade([]byte("B"), []byte("X"), 8))
	wantErr(t, c.Trade([]byte("B"), []byte("Y"), 1), limit.ErrLimitExceeded)
	wantExposure(t, c, "B", 80)
}

// TestReduceWhileBreached covers reducing a position while over limit
// (accepted even though E' > L) and increasing it (rejected).
func TestReduceWhileBreached(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("C"), 100))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.Trade([]byte("C"), []byte("X"), 10))
	wantExposure(t, c, "C", 100)

	must(t, c.SetPrice([]byte("X"), 15))
	wantExposure(t, c, "C", 150)
	wantBreaches(t, c, breachWant{"C", 150})

	must(t, c.Trade([]byte("C"), []byte("X"), -2))
	wantExposure(t, c, "C", 120)
	wantBreaches(t, c, breachWant{"C", 120})

	wantErr(t, c.Trade([]byte("C"), []byte("X"), 1), limit.ErrLimitExceeded)
	wantExposure(t, c, "C", 120)
}

// TestEqualExposureAccepted covers E' == E while E > L: a trade that
// increases W but decreases the buffer by the same amount is accepted.
func TestEqualExposureAccepted(t *testing.T) {
	c := newCtrl(t, 10000)
	must(t, c.Register([]byte("C"), 120))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 10))
	must(t, c.SetGroup([]byte("X"), []byte("g")))
	must(t, c.SetGroup([]byte("Y"), []byte("g")))

	must(t, c.Trade([]byte("C"), []byte("X"), 10))
	must(t, c.Trade([]byte("C"), []byte("Y"), -5))
	// W = 50, H = 50, W' = 100.
	wantExposure(t, c, "C", 100)

	must(t, c.SetPrice([]byte("X"), 15))
	// W = 100, H = 50, W' = 150 > 120: breached.
	wantExposure(t, c, "C", 150)
	wantBreaches(t, c, breachWant{"C", 150})

	// Buying back one short: W = 110, H = 40, W' = 150 == E: accepted.
	must(t, c.Trade([]byte("C"), []byte("Y"), 1))
	wantExposure(t, c, "C", 150)
	wantBreaches(t, c, breachWant{"C", 150})
}

// TestHedgeLowersExposure covers a short in another instrument reducing W
// enough to be accepted while the counterparty stays over limit.
func TestHedgeLowersExposure(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("C"), 100))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 10))
	must(t, c.Trade([]byte("C"), []byte("X"), 10))

	must(t, c.SetPrice([]byte("X"), 15))
	wantExposure(t, c, "C", 150)

	must(t, c.Trade([]byte("C"), []byte("Y"), -2))
	wantExposure(t, c, "C", 130)
	wantBreaches(t, c, breachWant{"C", 130})
}

// TestLambdaExtremes covers lambda = 0 (no buffer) and lambda = 10000
// (buffer equals the hedged value).
func TestLambdaExtremes(t *testing.T) {
	setup := func(lambda int64) *limit.Controller {
		c := newCtrl(t, lambda)
		must(t, c.Register([]byte("C"), 1_000_000_000_000_000))
		must(t, c.SetPrice([]byte("X"), 10))
		must(t, c.SetPrice([]byte("Y"), 10))
		must(t, c.SetGroup([]byte("X"), []byte("g")))
		must(t, c.SetGroup([]byte("Y"), []byte("g")))
		must(t, c.Trade([]byte("C"), []byte("X"), 7))
		must(t, c.Trade([]byte("C"), []byte("Y"), -5))
		return c
	}
	// W = 20, H = 50.
	wantExposure(t, setup(0), "C", 20)
	wantExposure(t, setup(10000), "C", 70)
}

// TestSameGroupVsSeparateGroups measures the W' difference of the same
// long/short pair placed in one group versus two groups.
func TestSameGroupVsSeparateGroups(t *testing.T) {
	build := func(group bool) *limit.Controller {
		c := newCtrl(t, 1000)
		must(t, c.Register([]byte("C"), 1_000_000_000_000_000))
		must(t, c.SetPrice([]byte("X"), 10))
		must(t, c.SetPrice([]byte("Y"), 10))
		if group {
			must(t, c.SetGroup([]byte("X"), []byte("g")))
			must(t, c.SetGroup([]byte("Y"), []byte("g")))
		}
		must(t, c.Trade([]byte("C"), []byte("X"), 7))
		must(t, c.Trade([]byte("C"), []byte("Y"), -5))
		return c
	}
	// Same group: W' = 20 + ceil(1000*50/10000) = 25.
	wantExposure(t, build(true), "C", 25)
	// Separate singleton groups: no hedge, W' = W = 20.
	wantExposure(t, build(false), "C", 20)
}

// TestSetGroupPassiveBreach covers SetGroup pushing a counterparty over
// its limit without any trade.
func TestSetGroupPassiveBreach(t *testing.T) {
	c := newCtrl(t, 10000)
	must(t, c.Register([]byte("C"), 100))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 10))
	must(t, c.SetPrice([]byte("Z"), 10))
	must(t, c.SetGroup([]byte("X"), []byte("g1")))
	must(t, c.SetGroup([]byte("Y"), []byte("g2")))
	must(t, c.Trade([]byte("C"), []byte("X"), 10))
	must(t, c.Trade([]byte("C"), []byte("Y"), -10))
	must(t, c.Trade([]byte("C"), []byte("Z"), 10))
	// W = 100, no hedged group, W' = 100, E = 100 <= L.
	wantExposure(t, c, "C", 100)
	wantBreaches(t, c)

	// Merging Y into g1 hedges 100: W' = 100 + 100 = 200 > 100.
	must(t, c.SetGroup([]byte("Y"), []byte("g1")))
	wantExposure(t, c, "C", 200)
	wantBreaches(t, c, breachWant{"C", 200})

	// Splitting again removes the buffer and the breach.
	must(t, c.SetGroup([]byte("Y"), []byte("g2")))
	wantExposure(t, c, "C", 100)
	wantBreaches(t, c)
}

// TestNegativeWExposureZero covers W < 0 (E = 0) and a further trade whose
// W' stays below the posted collateral.
func TestNegativeWExposureZero(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("C"), 10))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 10))
	must(t, c.Trade([]byte("C"), []byte("Y"), -5))
	wantExposure(t, c, "C", 0)

	must(t, c.Post([]byte("C"), 100))
	must(t, c.Trade([]byte("C"), []byte("X"), 5))
	// W = 0 <= G = 100: E stays 0.
	wantExposure(t, c, "C", 0)
	wantBreaches(t, c)
}

// TestNegativeWAddRejected covers a counterparty with W < 0 whose buy
// pushes E' past the limit.
func TestNegativeWAddRejected(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("C"), 10))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 10))
	must(t, c.Trade([]byte("C"), []byte("Y"), -5))
	wantExposure(t, c, "C", 0)

	wantErr(t, c.Trade([]byte("C"), []byte("X"), 10), limit.ErrLimitExceeded)
	wantExposure(t, c, "C", 0)

	must(t, c.Trade([]byte("C"), []byte("X"), 6))
	wantExposure(t, c, "C", 10)
}

// TestReleaseLimit covers releases rejected for pushing E' over the limit
// and releases accepted because they do not increase exposure.
func TestReleaseLimit(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("C"), 50))
	must(t, c.Post([]byte("C"), 100))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.Trade([]byte("C"), []byte("X"), 10))
	wantExposure(t, c, "C", 0)

	wantErr(t, c.Release([]byte("C"), 60), limit.ErrLimitExceeded)
	must(t, c.Release([]byte("C"), 50))
	wantExposure(t, c, "C", 50)

	// W = 0 with G = 50: releasing 30 keeps W'-G < 0, so E stays 0.
	must(t, c.SetPrice([]byte("Y"), 10))
	must(t, c.Trade([]byte("C"), []byte("Y"), -10))
	must(t, c.Release([]byte("C"), 30))
	wantExposure(t, c, "C", 0)

	// Releasing more than the deposited collateral is a distinct reason.
	wantErr(t, c.Release([]byte("C"), 21), limit.ErrInsufficientCollateral)
	wantErr(t, c.Release([]byte("C"), 0), limit.ErrInvalidParam)
	wantErr(t, c.Release([]byte("D"), 1), limit.ErrNotRegistered)
}

// TestNoPriceRejected covers trades and group assignments on unpriced
// instruments.
func TestNoPriceRejected(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("C"), 100))
	wantErr(t, c.Trade([]byte("C"), []byte("X"), 1), limit.ErrNoPrice)
	wantErr(t, c.SetGroup([]byte("X"), []byte("g")), limit.ErrNoPrice)
	wantErr(t, c.SetGroup([]byte("X"), nil), limit.ErrInvalidParam)
	wantErr(t, c.Trade([]byte("D"), []byte("X"), 1), limit.ErrNotRegistered)
	wantErr(t, c.Trade([]byte("C"), []byte("X"), 0), limit.ErrInvalidParam)
	wantErr(t, c.Trade([]byte("C"), nil, 1), limit.ErrInvalidParam)
}

// TestMultiBreachSeqOrder covers several counterparties newly breaching on
// the same operation: sequence numbers follow byte order of the names.
func TestMultiBreachSeqOrder(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("B"), 50))
	must(t, c.Register([]byte("A"), 50))
	must(t, c.Register([]byte("D"), 50))
	must(t, c.SetPrice([]byte("X"), 1))
	for _, cp := range []string{"A", "B", "D"} {
		must(t, c.Trade([]byte(cp), []byte("X"), 10))
	}
	must(t, c.SetPrice([]byte("X"), 10))
	wantBreaches(t, c,
		breachWant{"A", 100},
		breachWant{"B", 100},
		breachWant{"D", 100},
	)
	got := c.Breaches()
	for i, wantSeq := range []uint64{1, 2, 3} {
		if got[i].Seq != wantSeq {
			t.Fatalf("Breaches()[%d].Seq = %d, want %d", i, got[i].Seq, wantSeq)
		}
	}
}

// TestReenterGetsNewSeq covers a counterparty leaving and re-entering the
// breach list with a fresh sequence number while a persistent breacher
// keeps its original one.
func TestReenterGetsNewSeq(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("A"), 50))
	must(t, c.Register([]byte("B"), 50))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 10))
	must(t, c.Trade([]byte("A"), []byte("X"), 4))
	must(t, c.Trade([]byte("B"), []byte("Y"), 4))

	must(t, c.SetPrice([]byte("X"), 20))
	must(t, c.SetPrice([]byte("Y"), 20))
	wantBreaches(t, c, breachWant{"A", 80}, breachWant{"B", 80})
	if seq := c.Breaches()[1].Seq; seq != 2 {
		t.Fatalf("B seq = %d, want 2", seq)
	}

	must(t, c.Post([]byte("A"), 40))
	wantBreaches(t, c, breachWant{"B", 80})

	must(t, c.SetPrice([]byte("X"), 30))
	wantBreaches(t, c, breachWant{"B", 80}, breachWant{"A", 80})
	got := c.Breaches()
	if got[0].Seq != 2 || got[1].Seq != 3 {
		t.Fatalf("seqs = (%d, %d), want (2, 3)", got[0].Seq, got[1].Seq)
	}
}

// TestParamValidation covers out-of-range parameters across operations.
func TestParamValidation(t *testing.T) {
	c := newCtrl(t, 0)
	wantErr(t, c.Register(nil, 10), limit.ErrInvalidParam)
	wantErr(t, c.Register([]byte("C"), -1), limit.ErrInvalidParam)
	wantErr(t, c.Register([]byte("C"), 1_000_000_000_000_001), limit.ErrInvalidParam)
	must(t, c.Register([]byte("C"), 1_000_000_000_000_000))
	wantErr(t, c.Register([]byte("C"), 1), limit.ErrDuplicateCounterparty)

	wantErr(t, c.SetPrice(nil, 1), limit.ErrInvalidParam)
	wantErr(t, c.SetPrice([]byte("X"), 0), limit.ErrInvalidParam)
	wantErr(t, c.SetPrice([]byte("X"), 1_000_001), limit.ErrInvalidParam)
	must(t, c.SetPrice([]byte("X"), 1_000_000))

	wantErr(t, c.Trade([]byte("C"), []byte("X"), 1_000_001), limit.ErrInvalidParam)
	wantErr(t, c.Trade([]byte("C"), []byte("X"), -1_000_001), limit.ErrInvalidParam)
	must(t, c.Trade([]byte("C"), []byte("X"), 1_000_000))
	wantErr(t, c.Trade([]byte("C"), []byte("X"), 1), limit.ErrInvalidParam)
	must(t, c.Trade([]byte("C"), []byte("X"), -1_000_000))
	must(t, c.Trade([]byte("C"), []byte("X"), -1_000_000))
	wantErr(t, c.Trade([]byte("C"), []byte("X"), -1), limit.ErrInvalidParam)

	wantErr(t, c.Post([]byte("C"), 0), limit.ErrInvalidParam)
	wantErr(t, c.Post([]byte("C"), 1_000_000_000_001), limit.ErrInvalidParam)
	wantErr(t, c.Post([]byte("D"), 1), limit.ErrNotRegistered)
	for i := 0; i < 1000; i++ {
		must(t, c.Post([]byte("C"), 1_000_000_000_000))
	}
	wantErr(t, c.Post([]byte("C"), 1), limit.ErrInvalidParam)
}

// TestInstrumentCountLimit covers the 1000-instrument bound.
func TestInstrumentCountLimit(t *testing.T) {
	c := newCtrl(t, 0)
	for i := 0; i < 1000; i++ {
		name := []byte{byte('a' + i/26), byte('a' + i%26), byte(i)}
		must(t, c.SetPrice(name, 1))
	}
	wantErr(t, c.SetPrice([]byte("overflow"), 1), limit.ErrInvalidParam)
	// Updating an existing instrument still works.
	must(t, c.SetPrice([]byte{'a' + 0, 'a' + 0, 0}, 5))
}

// TestRejectionNoStateChange verifies that rejected operations leave
// positions, collateral, prices, groups, marks and the sequence counter
// untouched.
func TestRejectionNoStateChange(t *testing.T) {
	c := newCtrl(t, 0)
	must(t, c.Register([]byte("C"), 100))
	must(t, c.Register([]byte("D"), 50))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 20))
	must(t, c.Trade([]byte("C"), []byte("X"), 8))
	must(t, c.Post([]byte("D"), 10))

	snapshot := func() (string, string) {
		ec, _ := c.Exposure([]byte("C"))
		ed, _ := c.Exposure([]byte("D"))
		return ec.String() + "/" + ed.String(), fmt.Sprint(c.Breaches())
	}
	beforeExp, beforeBreaches := snapshot()

	wantErr(t, c.Trade([]byte("C"), []byte("Y"), 2), limit.ErrLimitExceeded)
	wantErr(t, c.Trade([]byte("C"), []byte("ZZ"), 1), limit.ErrNoPrice)
	wantErr(t, c.Trade([]byte("ZZ"), []byte("X"), 1), limit.ErrNotRegistered)
	wantErr(t, c.Trade([]byte("C"), []byte("X"), 1_000_000), limit.ErrInvalidParam)
	wantErr(t, c.Release([]byte("D"), 11), limit.ErrInsufficientCollateral)
	wantErr(t, c.Release([]byte("D"), 0), limit.ErrInvalidParam)
	wantErr(t, c.Post([]byte("D"), 1_000_000_000_001), limit.ErrInvalidParam)
	wantErr(t, c.Register([]byte("C"), 5), limit.ErrDuplicateCounterparty)
	wantErr(t, c.SetPrice([]byte("X"), 0), limit.ErrInvalidParam)
	wantErr(t, c.SetGroup([]byte("ZZ"), []byte("g")), limit.ErrNoPrice)

	afterExp, afterBreaches := snapshot()
	if beforeExp != afterExp || beforeBreaches != afterBreaches {
		t.Fatalf("state changed: (%s, %s) -> (%s, %s)",
			beforeExp, beforeBreaches, afterExp, afterBreaches)
	}

	// The sequence counter must not have been consumed by rejections:
	// the next breach takes sequence number 1.
	must(t, c.SetPrice([]byte("X"), 20))
	got := c.Breaches()
	if len(got) != 1 || got[0].Seq != 1 {
		t.Fatalf("Breaches() = %+v, want one entry with seq 1", got)
	}
}

// TestConcurrent hammers the controller from many goroutines and checks
// the global invariants afterwards. Run with -race to validate the
// locking.
func TestConcurrent(t *testing.T) {
	c := newCtrl(t, 1000)
	must(t, c.Register([]byte("C"), 1_000_000))
	must(t, c.Register([]byte("D"), 1_000_000))
	must(t, c.SetPrice([]byte("X"), 10))
	must(t, c.SetPrice([]byte("Y"), 7))

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			cps := []string{"C", "D"}
			insts := []string{"X", "Y"}
			for i := 0; i < 500; i++ {
				cp := []byte(cps[r.Intn(2)])
				inst := []byte(insts[r.Intn(2)])
				switch r.Intn(6) {
				case 0:
					_ = c.Trade(cp, inst, int64(r.Intn(11)-5))
				case 1:
					_ = c.Post(cp, int64(r.Intn(100)+1))
				case 2:
					_ = c.Release(cp, int64(r.Intn(50)+1))
				case 3:
					_ = c.SetPrice(inst, int64(r.Intn(20)+1))
				case 4:
					_ = c.SetGroup(inst, []byte("g"))
				default:
					_, _ = c.Exposure(cp)
					_ = c.Breaches()
				}
			}
		}(int64(worker))
	}
	wg.Wait()

	for _, cp := range []string{"C", "D"} {
		e, err := c.Exposure([]byte(cp))
		if err != nil {
			t.Fatalf("Exposure(%s): %v", cp, err)
		}
		if e.Sign() < 0 {
			t.Fatalf("Exposure(%s) = %s, want non-negative", cp, e)
		}
	}
	seqs := make(map[uint64]bool)
	for _, b := range c.Breaches() {
		if seqs[b.Seq] {
			t.Fatalf("duplicate breach seq %d", b.Seq)
		}
		seqs[b.Seq] = true
	}
}
