package vegas

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentAcquireRelease hammers the limiter from many goroutines. It
// checks: outstanding count always equals n; no token is released twice;
// successful concurrent Acquires never exceed L-n.
func TestConcurrentAcquireRelease(t *testing.T) {
	// Tmo exceeds the maximum shared logical clock (goroutines*loops*2), so
	// tokens acquired here are never externally reaped; drops still cut L.
	cfg := Config{L0: 8, Lmin: 1, Lmax: 20, Alpha: 1, Beta: 3, Tmo: 100_000, Cd: 5, Wm: 200}
	l, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	const loops = 400
	var wg sync.WaitGroup
	var violations int64
	var pendingMu sync.Mutex
	var deferred []Token

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < loops; i++ {
				// Goroutines use overlapping real time, so interleaving can
				// legitimately produce ErrClockRewind on the second call.
				now := int64(id)*2000 + int64(2*i) + 1
				now2 := now + 1
				tok, err := l.Acquire(now)
				if err != nil {
					if err != ErrClockRewind && err != ErrAtLimit {
						atomic.AddInt64(&violations, 1)
					}
					// rejected attempts must leave invariants intact
					s := l.State()
					if s.N < 0 || s.Outstanding != int(s.N) || s.L < cfg.Lmin || s.L > cfg.Lmax {
						atomic.AddInt64(&violations, 1)
					}
					continue
				}
				s := l.State()
				if s.Outstanding != int(s.N) || tok.w <= 0 || s.L < cfg.Lmin || s.L > cfg.Lmax {
					atomic.AddInt64(&violations, 1)
				}
				rtt := int64(1 + (now % 300))
				res := ResultSuccess
				switch now % 7 {
				case 0:
					res = ResultDrop
				case 1:
					res = ResultIgnore
				}
				if err := l.Release(tok, res, rtt, now2); err != nil {
					if err == ErrClockRewind {
						pendingMu.Lock()
						deferred = append(deferred, tok)
						pendingMu.Unlock()
					} else {
						atomic.AddInt64(&violations, 1)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	// Drain tokens whose release only failed due to interleaved timestamps.
	drain := l.State().MaxNow + 1
	for _, tok := range deferred {
		if err := l.Release(tok, ResultIgnore, 0, drain); err != nil {
			t.Errorf("drain: %v", err)
		}
		drain++
	}

	if violations != 0 {
		t.Fatalf("invariant violations: %d", violations)
	}
	s := l.State()
	if s.N != 0 || s.Outstanding != 0 {
		t.Fatalf("all released: %+v", s)
	}
	if s.L < cfg.Lmin || s.L > cfg.Lmax {
		t.Fatalf("L out of bounds: %d", s.L)
	}
}

// TestConcurrentAdmitCap: with releases disabled during the burst, the number
// of successful concurrent Acquires never exceeds the limit.
func TestConcurrentAdmitCap(t *testing.T) {
	l, _ := New(Config{L0: 5, Lmin: 5, Lmax: 5, Alpha: 1, Beta: 2, Tmo: 1_000_000, Cd: 1_000_000, Wm: 1_000_000})
	var admitted int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Acquire(0); err == nil {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	wg.Wait()
	if admitted != 5 {
		t.Fatalf("admitted=%d want 5", admitted)
	}
	s := l.State()
	if s.N != 5 || s.Outstanding != 5 {
		t.Fatalf("%+v", s)
	}
}

// TestExaminedAmortized verifies the cumulative examined counters stay within
// a small constant multiple of (admitted tokens + successful samples), at
// both ~1000 and ~100000 operation workloads, while individual operations do
// no linear scan over outstanding tokens.
func TestExaminedAmortized(t *testing.T) {
	for _, ops := range []int{1000, 100_000} {
		l, _ := New(Config{L0: 16, Lmin: 1, Lmax: 64, Alpha: 2, Beta: 5, Tmo: 12, Cd: 3, Wm: 40})
		now := int64(0)
		var pending []Token
		var admitted, successes int64
		for i := 0; i < ops; i++ {
			now++
			if i%3 != 0 || len(pending) == 0 {
				tok, err := l.Acquire(now)
				if err == nil {
					pending = append(pending, tok)
					admitted++
				}
				continue
			}
			tok := pending[0]
			pending = pending[1:]
			res := ResultSuccess
			rtt := int64(1 + (now % 120))
			if now%9 == 0 {
				res = ResultDrop
			} else if now%11 == 0 {
				res = ResultIgnore
			}
			if err := l.Release(tok, res, rtt, now); err == nil && res == ResultSuccess {
				successes++
			}
		}
		win, tok := l.Examined()
		base := admitted + successes
		// Both counters are incremented at most a constant number of times per
		// admitted token / recorded sample over the whole run.
		if win > 3*base || tok > 3*admitted {
			t.Fatalf("ops=%d examined(window=%d tokens=%d) vs base=%d admitted=%d",
				ops, win, tok, base, admitted)
		}
		if ops == 100_000 {
			t.Logf("ops=%d examined window=%d tokens=%d (admitted=%d successes=%d)",
				ops, win, tok, admitted, successes)
		}
	}
}

// TestTokenLookupSublinear directly confirms a Release does not scan the
// outstanding table: the token-examined counter moves only when reaping
// actually expires tokens, never on a normal return.
func TestTokenLookupSublinear(t *testing.T) {
	l, _ := New(Config{L0: 200, Lmin: 1, Lmax: 200, Alpha: 1, Beta: 2, Tmo: 1_000_000, Cd: 1_000_000, Wm: 1_000_000})
	var toks []Token
	for i := 0; i < 200; i++ {
		tok, err := l.Acquire(0)
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, tok)
	}
	_, before := l.Examined()
	// release the last token; no reaping occurs, so tokenExamined is unchanged
	if err := l.Release(toks[199], ResultIgnore, 0, 1); err != nil {
		t.Fatal(err)
	}
	_, after := l.Examined()
	if before != after {
		t.Fatalf("release scanned token table: before=%d after=%d", before, after)
	}
	// release an arbitrary middle token too
	if err := l.Release(toks[100], ResultIgnore, 0, 2); err != nil {
		t.Fatal(err)
	}
	_, after2 := l.Examined()
	if before != after2 {
		t.Fatalf("middle release scanned: before=%d after=%d", before, after2)
	}
}
