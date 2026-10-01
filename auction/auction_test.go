package auction

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Auction {
	t.Helper()
	a, err := NewAuction(cfg)
	if err != nil {
		t.Fatalf("NewAuction(%+v) = %v, want nil", cfg, err)
	}
	return a
}

func mustBid(t *testing.T, a *Auction, bidder string, m, now int64) {
	t.Helper()
	if err := a.Bid(bidder, m, now); err != nil {
		t.Fatalf("Bid(%q, %d, %d) = %v, want nil", bidder, m, now, err)
	}
}

func wantBidErr(t *testing.T, a *Auction, bidder string, m, now int64, want error) {
	t.Helper()
	if err := a.Bid(bidder, m, now); !errors.Is(err, want) {
		t.Fatalf("Bid(%q, %d, %d) = %v, want %v", bidder, m, now, err, want)
	}
}

func wantSnap(t *testing.T, a *Auction, price int64, leader string, end int64) {
	t.Helper()
	s := a.Snapshot()
	if s.Price != price || s.Leader != leader || s.EndTime != end {
		t.Fatalf("Snapshot = {P:%d leader:%q E:%d}, want {P:%d leader:%q E:%d}",
			s.Price, s.Leader, s.EndTime, price, leader, end)
	}
}

func TestConfigValidation(t *testing.T) {
	base := Config{StartPrice: 10, MinIncrement: 5, ReservePrice: 0, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	if _, err := NewAuction(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []Config{
		{StartPrice: 0, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20},                               // S < 1
		{StartPrice: 10, MinIncrement: 0, EndTime: 100, ExtendWindow: 10, ExtendBy: 20},                              // D < 1
		{StartPrice: 10, MinIncrement: 5, ReservePrice: -1, EndTime: 100, ExtendWindow: 10, ExtendBy: 20},            // R < 0
		{StartPrice: 10, MinIncrement: 5, EndTime: 0, ExtendWindow: 10, ExtendBy: 20},                                // E < 1
		{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: -1, ExtendBy: 20},                              // W < 0
		{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 0},                               // X < 1
		{StartPrice: MaxAmount + 1, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20},                   // S > 1e15
		{StartPrice: 10, MinIncrement: MaxAmount + 1, EndTime: 100, ExtendWindow: 10, ExtendBy: 20},                  // D > 1e15
		{StartPrice: 10, MinIncrement: 5, ReservePrice: MaxAmount + 1, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}, // R > 1e15
	}
	for i, cfg := range bad {
		if _, err := NewAuction(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("bad config %d: NewAuction = %v, want ErrInvalidConfig", i, err)
		}
	}
}

func TestFirstBidThreshold(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)
	wantBidErr(t, a, "alice", 9, 0, ErrBelowThreshold) // S-1 rejected
	wantSnap(t, a, 0, "", 100)                         // rejection changed nothing
	mustBid(t, a, "alice", 10, 0)                      // exactly S accepted
	wantSnap(t, a, 10, "alice", 100)
}

func TestBidRejectionReasonsAndOrder(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)

	// Invalid parameters.
	wantBidErr(t, a, "", 10, 0, ErrInvalidBidParam)               // empty bidder
	wantBidErr(t, a, "alice", 0, 0, ErrInvalidBidParam)           // m < 1
	wantBidErr(t, a, "alice", MaxAmount+1, 0, ErrInvalidBidParam) // m > 1e15
	// Invalid param wins over clock regression and ended.
	mustBid(t, a, "alice", 20, 5)
	wantBidErr(t, a, "", 20, 4, ErrInvalidBidParam)
	wantBidErr(t, a, "", 20, 200, ErrInvalidBidParam)

	// Clock regression.
	wantBidErr(t, a, "bob", 30, -1, ErrClockBackward) // now < 0
	wantBidErr(t, a, "bob", 30, 4, ErrClockBackward)  // now < max accepted now (5)
	// Clock regression wins over ended: use an auction already past E.
	b := mustNew(t, cfg)
	mustBid(t, b, "alice", 20, 5)
	wantBidErr(t, b, "bob", 30, 4, ErrClockBackward)
	wantBidErr(t, b, "bob", 30, 100, ErrAuctionEnded) // now == E rejected
	wantBidErr(t, b, "bob", 30, 150, ErrAuctionEnded) // now > E rejected

	// Settled auction rejects bids even before E.
	c := mustNew(t, cfg)
	mustBid(t, c, "alice", 20, 0)
	if _, err := c.Settle(100); err != nil {
		t.Fatalf("Settle = %v, want nil", err)
	}
	wantBidErr(t, c, "bob", 30, 1, ErrAuctionEnded)

	// Leader not raising max.
	wantBidErr(t, c, "alice", 20, 1, ErrAuctionEnded) // settled wins over leader check
	d := mustNew(t, cfg)
	mustBid(t, d, "alice", 20, 0)
	wantBidErr(t, d, "alice", 20, 1, ErrNotRaisingMax) // m == M
	wantBidErr(t, d, "alice", 15, 1, ErrNotRaisingMax) // m < M
}

func TestChallengerThreshold(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)
	mustBid(t, a, "alice", 20, 0)                     // P=10, M=20
	wantBidErr(t, a, "bob", 14, 1, ErrBelowThreshold) // P+D-1 = 14 rejected
	wantSnap(t, a, 10, "alice", 100)
	mustBid(t, a, "bob", 15, 1)      // exactly P+D accepted; m=15 <= M=20
	wantSnap(t, a, 20, "alice", 100) // P = min(M, m+D) = min(20, 20) = 20
}

func TestNewLeaderWithPriceBelowOldMaxPlusIncrement(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)
	mustBid(t, a, "alice", 20, 0)  // P=10, M=20
	mustBid(t, a, "bob", 23, 1)    // m > M but m < M+D=25
	wantSnap(t, a, 23, "bob", 100) // P = min(m, oldM+D) = min(23, 25) = 23 = m
}

func TestNewLeaderWithPriceCappedByOldMaxPlusIncrement(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)
	mustBid(t, a, "alice", 20, 0)  // P=10, M=20
	mustBid(t, a, "bob", 30, 1)    // m > M and m > M+D=25
	wantSnap(t, a, 25, "bob", 100) // P = min(m, oldM+D) = min(30, 25) = 25
}

func TestEqualMaxKeepsFirstBidder(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)
	mustBid(t, a, "alice", 20, 0)    // P=10, M=20
	mustBid(t, a, "bob", 20, 1)      // m == M: first bidder keeps the lead
	wantSnap(t, a, 20, "alice", 100) // P = min(M, m+D) = min(20, 25) = 20 = M
}

func TestLowerChallengeRaisesPrice(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)
	mustBid(t, a, "alice", 30, 0) // P=10, M=30
	mustBid(t, a, "bob", 16, 1)   // m < M: P = min(M, m+D) = min(30, 21) = 21
	wantSnap(t, a, 21, "alice", 100)
	mustBid(t, a, "carol", 26, 2) // m < M: P = min(M, m+D) = min(30, 31) = 30
	wantSnap(t, a, 30, "alice", 100)
}

func TestLeaderSelfRaise(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)
	mustBid(t, a, "alice", 20, 0)  // P=10, M=20
	mustBid(t, a, "alice", 50, 95) // inside window (E-now=5 <= W=10): self-raise never extends
	wantSnap(t, a, 10, "alice", 100)
	// The raised max is now effective against challengers.
	mustBid(t, a, "bob", 40, 96)     // m=40 <= M=50: P = min(50, 40+5) = 45, alice keeps lead
	wantSnap(t, a, 45, "alice", 116) // non-leader bid inside window: E = max(100, 96+20) = 116
}

func TestSoftCloseExtensionBoundary(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}

	// E-now == W exactly: extends.
	a := mustNew(t, cfg)
	mustBid(t, a, "alice", 20, 90)   // first bid, E-now = 10 == W
	wantSnap(t, a, 10, "alice", 110) // E = max(100, 90+20) = 110

	// E-now == W+1: no extension.
	b := mustNew(t, cfg)
	mustBid(t, b, "alice", 20, 89) // E-now = 11 > W
	wantSnap(t, b, 10, "alice", 100)

	// now+X < E: inside the window but E is not shortened.
	c := mustNew(t, Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 5})
	mustBid(t, c, "alice", 20, 95) // E-now = 5 <= W, now+X = 100 == E
	wantSnap(t, c, 10, "alice", 100)
	d := mustNew(t, Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 3})
	mustBid(t, d, "alice", 20, 95) // now+X = 98 < E
	wantSnap(t, d, 10, "alice", 100)

	// Non-leader challenge inside the window extends; leader self-raise does not.
	e := mustNew(t, cfg)
	mustBid(t, e, "alice", 20, 0)
	mustBid(t, e, "alice", 30, 95) // self-raise inside window: no extension
	wantSnap(t, e, 10, "alice", 100)
	mustBid(t, e, "bob", 25, 96)     // challenger inside window: extends
	wantSnap(t, e, 30, "alice", 116) // P = min(M=30, 25+5) = 30, E = max(100, 96+20) = 116
}

func TestSettle(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, ReservePrice: 20, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}

	// Too early.
	a := mustNew(t, cfg)
	if _, err := a.Settle(99); !errors.Is(err, ErrNotEnded) {
		t.Fatalf("Settle(99) = %v, want ErrNotEnded", err)
	}

	// No leader: not sold.
	r, err := a.Settle(100)
	if err != nil || r.Sold {
		t.Fatalf("Settle with no leader = %+v, %v; want not sold", r, err)
	}

	// M < R: not sold.
	b := mustNew(t, cfg)
	mustBid(t, b, "alice", 15, 0) // M=15 < R=20
	r, err = b.Settle(100)
	if err != nil || r.Sold {
		t.Fatalf("Settle with M<R = %+v, %v; want not sold", r, err)
	}

	// M == R exactly: sold, price = max(P, R) = R since P=10 < R.
	c := mustNew(t, cfg)
	mustBid(t, c, "alice", 20, 0) // M=20 == R
	r, err = c.Settle(100)
	if err != nil || !r.Sold || r.Winner != "alice" || r.Price != 20 {
		t.Fatalf("Settle with M==R = %+v, %v; want sold to alice at 20", r, err)
	}

	// P < R <= M: price is raised to R.
	d := mustNew(t, cfg)
	mustBid(t, d, "alice", 50, 0) // P=10 < R=20 <= M=50
	r, err = d.Settle(100)
	if err != nil || !r.Sold || r.Price != 20 {
		t.Fatalf("Settle with P<R<=M = %+v, %v; want price 20", r, err)
	}

	// P >= R: price stays P.
	e := mustNew(t, cfg)
	mustBid(t, e, "alice", 50, 0)
	mustBid(t, e, "bob", 40, 1) // P = min(50, 45) = 45
	r, err = e.Settle(100)
	if err != nil || !r.Sold || r.Winner != "alice" || r.Price != 45 {
		t.Fatalf("Settle with P>R = %+v, %v; want alice at 45", r, err)
	}
	re := r

	// No reserve (R=0): always sold when there is a leader.
	f := mustNew(t, Config{StartPrice: 10, MinIncrement: 5, EndTime: 100, ExtendWindow: 10, ExtendBy: 20})
	mustBid(t, f, "alice", 10, 0)
	r, err = f.Settle(100)
	if err != nil || !r.Sold || r.Price != 10 {
		t.Fatalf("Settle with R=0 = %+v, %v; want sold at 10", r, err)
	}

	// Result is stable: repeated calls with any now return the same result.
	r2, err := e.Settle(1000000)
	if err != nil || r2 != re {
		t.Fatalf("repeated Settle = %+v, %v; want %+v", r2, err, re)
	}
	r3, err := e.Settle(0)
	if err != nil || r3 != re {
		t.Fatalf("Settle after result with now<E = %+v, %v; want %+v", r3, err, re)
	}
}

func TestRejectedBidKeepsState(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, ReservePrice: 0, EndTime: 100, ExtendWindow: 10, ExtendBy: 20}
	a := mustNew(t, cfg)
	mustBid(t, a, "alice", 20, 5)
	before := a.Snapshot()

	rejections := []struct {
		bidder string
		m, now int64
	}{
		{"", 30, 6},      // invalid param
		{"bob", 0, 6},    // invalid param
		{"bob", 30, 4},   // clock regression
		{"bob", 1, 6},    // below threshold (P+D = 15)
		{"alice", 20, 6}, // leader not raising max
		{"bob", 30, 100}, // ended
	}
	for i, rj := range rejections {
		if err := a.Bid(rj.bidder, rj.m, rj.now); err == nil {
			t.Fatalf("rejection %d unexpectedly accepted", i)
		}
		if got := a.Snapshot(); got != before {
			t.Fatalf("rejection %d changed state: %+v -> %+v", i, before, got)
		}
	}
	// maxSeenNow unchanged: a bid at now=5 (equal to last accepted) is still allowed.
	mustBid(t, a, "bob", 30, 5)
	wantSnap(t, a, 25, "bob", 100)
}

func TestConcurrentAccess(t *testing.T) {
	cfg := Config{StartPrice: 10, MinIncrement: 5, ReservePrice: 0, EndTime: 50, ExtendWindow: 10, ExtendBy: 5}
	a := mustNew(t, cfg)

	var clock atomic.Int64
	var wg sync.WaitGroup
	results := make(chan Result, 64)

	// Bidders: now values come from a shared atomic counter so most bids
	// are clock-valid; some will still lose the race to the end time.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			bidder := string(rune('a' + g))
			for i := 0; i < 50; i++ {
				now := clock.Add(1) - 1
				_ = a.Bid(bidder, int64(10+g*50+i), now)
				_ = a.Snapshot()
			}
		}(g)
	}
	// Settlers: all must observe exactly one identical result.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				r, err := a.Settle(10_000)
				if err == nil {
					results <- r
					return
				}
			}
		}()
	}
	wg.Wait()
	close(results)

	var first Result
	n := 0
	for r := range results {
		if n == 0 {
			first = r
		} else if r != first {
			t.Fatalf("concurrent Settle produced divergent results: %+v vs %+v", first, r)
		}
		n++
	}
	if n != 8 {
		t.Fatalf("got %d settle results, want 8", n)
	}
	final, err := a.Settle(10_000)
	if err != nil || final != first {
		t.Fatalf("final Settle = %+v, %v; want %+v", final, err, first)
	}
	s := a.Snapshot()
	if !s.Settled || s.Result != first {
		t.Fatalf("Snapshot = %+v, want settled with result %+v", s, first)
	}
}

// model is a naive, step-by-step simulation of the auction rules, written
// independently from the Auction implementation. It is the reference for
// the randomized replay test.
type model struct {
	S, D, R, W, X int64
	P             int64
	leader        string
	hasLeader     bool
	M             int64
	E             int64
	maxNow        int64
	settled       bool
	result        Result
}

func newModel(cfg Config) *model {
	return &model{S: cfg.StartPrice, D: cfg.MinIncrement, R: cfg.ReservePrice,
		W: cfg.ExtendWindow, X: cfg.ExtendBy, E: cfg.EndTime}
}

func (m *model) extend(now int64) {
	if m.E-now <= m.W {
		if t := now + m.X; t > m.E {
			m.E = t
		}
	}
}

func (m *model) bid(bidder string, b, now int64) error {
	if bidder == "" || b < 1 || b > MaxAmount {
		return ErrInvalidBidParam
	}
	if now < 0 || now < m.maxNow {
		return ErrClockBackward
	}
	if now >= m.E || m.settled {
		return ErrAuctionEnded
	}
	if !m.hasLeader {
		if b < m.S {
			return ErrBelowThreshold
		}
		m.P = m.S
		m.leader = bidder
		m.hasLeader = true
		m.M = b
		m.extend(now)
		m.maxNow = now
		return nil
	}
	if bidder == m.leader {
		if b <= m.M {
			return ErrNotRaisingMax
		}
		m.M = b
		m.maxNow = now
		return nil
	}
	if b < m.P+m.D {
		return ErrBelowThreshold
	}
	if b > m.M {
		p := m.M + m.D
		if b < p {
			p = b
		}
		m.P = p
		m.leader = bidder
		m.M = b
	} else {
		p := b + m.D
		if m.M < p {
			p = m.M
		}
		m.P = p
	}
	m.extend(now)
	m.maxNow = now
	return nil
}

func (m *model) settle(now int64) (Result, error) {
	if m.settled {
		return m.result, nil
	}
	if now < m.E {
		return Result{}, ErrNotEnded
	}
	m.settled = true
	if !m.hasLeader || m.M < m.R {
		m.result = Result{Sold: false}
	} else {
		p := m.P
		if m.R > p {
			p = m.R
		}
		m.result = Result{Sold: true, Winner: m.leader, Price: p}
	}
	return m.result, nil
}

func errReason(err error) string {
	if err == nil {
		return "accepted"
	}
	return err.Error()
}

// TestRandomReplayAgainstModel replays 2000 random bid sequences against
// both the real auction (twice, to prove replay determinism) and the naive
// model, comparing the error reason, P, leader and E after every bid, and
// the final settlement. Every step is logged with input, output and the
// decision basis.
func TestRandomReplayAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	bidders := []string{"alice", "bob", "carol", "dave", ""}

	for seq := 0; seq < 2000; seq++ {
		cfg := Config{
			StartPrice:   1 + rng.Int63n(20),
			MinIncrement: 1 + rng.Int63n(5),
			ReservePrice: rng.Int63n(25),
			EndTime:      5 + rng.Int63n(40),
			ExtendWindow: rng.Int63n(12),
			ExtendBy:     1 + rng.Int63n(15),
		}
		a1 := mustNew(t, cfg)
		a2 := mustNew(t, cfg)
		m := newModel(cfg)

		t.Logf("seq=%d config=%+v", seq, cfg)

		now := rng.Int63n(3)
		steps := 1 + rng.Intn(12)
		for step := 0; step < steps; step++ {
			switch r := rng.Intn(100); {
			case r < 8:
				now -= 1 + rng.Int63n(3) // clock regression attempt
			case r < 16:
				now = m.E + rng.Int63n(5) // jump at/past the end
			default:
				now += rng.Int63n(8)
			}
			bidder := bidders[rng.Intn(len(bidders))]
			var amount int64
			switch rng.Intn(100) {
			case 0:
				amount = 0
			case 1:
				amount = MaxAmount + 1
			default:
				amount = 1 + rng.Int63n(45)
			}

			preP, preM, preE, preMaxNow := m.P, m.M, m.E, m.maxNow

			err1 := a1.Bid(bidder, amount, now)
			err2 := a2.Bid(bidder, amount, now)
			merr := m.bid(bidder, amount, now)
			s1 := a1.Snapshot()

			t.Logf("seq=%d step=%d input=Bid(%q,%d,%d) output=%s P=%d leader=%q E=%d basis={preP=%d preM=%d preE=%d maxSeenNow=%d S=%d D=%d W=%d X=%d}",
				seq, step, bidder, amount, now, errReason(merr),
				m.P, m.leader, m.E, preP, preM, preE, preMaxNow, m.S, m.D, m.W, m.X)

			if errReason(err1) != errReason(merr) {
				t.Fatalf("seq=%d step=%d Bid(%q,%d,%d): auction=%v model=%v",
					seq, step, bidder, amount, now, err1, merr)
			}
			if errReason(err2) != errReason(err1) {
				t.Fatalf("seq=%d step=%d: replay diverged: %v vs %v", seq, step, err1, err2)
			}
			if s1.Price != m.P || s1.Leader != m.leader || s1.EndTime != m.E {
				t.Fatalf("seq=%d step=%d: state {P:%d leader:%q E:%d} != model {P:%d leader:%q E:%d}",
					seq, step, s1.Price, s1.Leader, s1.EndTime, m.P, m.leader, m.E)
			}
		}

		settleNow := m.E + rng.Int63n(3)
		r1, e1 := a1.Settle(settleNow)
		r2, _ := a2.Settle(settleNow)
		mr, me := m.settle(settleNow)
		t.Logf("seq=%d settle input=Settle(%d) output=%+v err=%v basis={M=%d R=%d P=%d}",
			seq, settleNow, mr, me, m.M, m.R, m.P)
		if errReason(e1) != errReason(me) || r1 != mr || r2 != mr {
			t.Fatalf("seq=%d settle: auction=(%+v,%v) replay=(%+v) model=(%+v,%v)",
				seq, r1, e1, r2, mr, me)
		}
		// Settlement is stable under repetition with arbitrary now.
		again, err := a1.Settle(settleNow + 1000)
		if err != nil || again != mr {
			t.Fatalf("seq=%d repeated settle = %+v, %v; want %+v", seq, again, err, mr)
		}
	}
}
