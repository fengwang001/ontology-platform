package retrybudget

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveBudget is a reference implementation written directly from the
// specification: it keeps every event and rescans the full ledgers on each
// operation. The optimized Budget must agree with it on every step.
type naiveBudget struct {
	wd, wc, d, c, r, mx, cm int64
	deps                    []int64
	wds                     []withdrawal
	maxNow                  int64
}

func newNaive(wd, wc, d, c, r, mx, cm int64) *naiveBudget {
	return &naiveBudget{wd: wd, wc: wc, d: d, c: c, r: r, mx: mx, cm: cm}
}

func (n *naiveBudget) checkTime(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < n.maxNow {
		return ErrClockRegression
	}
	return nil
}

// stats scans the ledgers and returns the number of valid deposits, the
// number of valid withdrawals and the sum of their frozen costs at now.
func (n *naiveBudget) stats(now int64) (validDeps, validWds, costSum int64) {
	for _, t := range n.deps {
		if t+n.wd > now {
			validDeps++
		}
	}
	for _, w := range n.wds {
		if w.t+n.wc > now {
			validWds++
			costSum += w.cost
		}
	}
	return validDeps, validWds, costSum
}

func (n *naiveBudget) balance(now int64) int64 {
	validDeps, _, costSum := n.stats(now)
	dep := validDeps
	if dep > n.cm {
		dep = n.cm
	}
	return n.r + n.d*dep - costSum
}

func (n *naiveBudget) request(now int64) error {
	if err := n.checkTime(now); err != nil {
		return err
	}
	n.deps = append(n.deps, now)
	n.maxNow = now
	return nil
}

// tryRetry returns the allowance plus the decision basis (k, cost, balance
// before the decision) for logging.
func (n *naiveBudget) tryRetry(now int64) (allowed bool, k, cost, bal int64, err error) {
	if err = n.checkTime(now); err != nil {
		return false, 0, 0, 0, err
	}
	_, k, _ = n.stats(now)
	mult := k + 1
	if mult > n.mx {
		mult = n.mx
	}
	cost = n.c * mult
	bal = n.balance(now)
	allowed = bal >= cost
	if allowed {
		n.wds = append(n.wds, withdrawal{t: now, cost: cost})
	}
	n.maxNow = now
	return allowed, k, cost, bal, nil
}

func (n *naiveBudget) balanceOp(now int64) (int64, error) {
	if err := n.checkTime(now); err != nil {
		return 0, err
	}
	n.maxNow = now
	return n.balance(now), nil
}

// randomParams draws a parameter set; the regime argument varies the
// scales so that some sequences exercise tiny windows (heavy expiry) and
// others large ones.
func randomParams(rng *rand.Rand, regime int) (wd, wc, d, c, r, mx, cm int64) {
	switch regime {
	case 0: // tiny windows: constant expiry
		wd, wc = 1+rng.Int63n(5), 1+rng.Int63n(5)
	case 1: // moderate windows
		wd, wc = 1+rng.Int63n(30), 1+rng.Int63n(30)
	default: // large windows: almost nothing expires
		wd, wc = 1+rng.Int63n(1_000_000_000), 1+rng.Int63n(1_000_000_000)
	}
	d = 1 + rng.Int63n(5)
	c = 1 + rng.Int63n(5)
	r = rng.Int63n(21)
	mx = 1 + rng.Int63n(10)
	cm = 1 + rng.Int63n(10)
	return wd, wc, d, c, r, mx, cm
}

// randomOp draws the next operation. now usually advances by a small
// non-negative delta; occasionally it regresses or leaves the legal range
// to exercise rejection paths.
func randomOp(rng *rand.Rand, now int64) (kind int, at int64) {
	kind = rng.Intn(3) // 0=Request 1=TryRetry 2=Balance
	at = now + rng.Int63n(4)
	switch roll := rng.Intn(100); {
	case roll < 4:
		at = now - rng.Int63n(10) // possible clock regression
	case roll < 6:
		at = -1 // invalid time
	case roll < 8:
		at = maxTime + 1 // invalid time
	}
	return kind, at
}

// TestRandomSequencesAgainstNaive replays 2000 random operation sequences
// against both the optimized Budget and the naive per-event-scan reference,
// requiring identical return values and balances at every step. Each step
// is logged with its input, output and decision basis.
func TestRandomSequencesAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		wd, wc, d, c, r, mx, cm := randomParams(rng, seq%3)
		real, err := New(wd, wc, d, c, r, mx, cm)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		naive := newNaive(wd, wc, d, c, r, mx, cm)
		t.Logf("seq=%d params Wd=%d Wc=%d D=%d C=%d R=%d Mx=%d Cm=%d",
			seq, wd, wc, d, c, r, mx, cm)

		now := int64(0)
		ops := 1 + rng.Intn(60)
		for op := 0; op < ops; op++ {
			kind, at := randomOp(rng, now)
			switch kind {
			case 0:
				realErr := real.Request(at)
				naiveErr := naive.request(at)
				t.Logf("seq=%d op=%d Request(%d) -> err=%v (naive err=%v)",
					seq, op, at, realErr, naiveErr)
				if (realErr == nil) != (naiveErr == nil) {
					t.Fatalf("seq %d op %d Request(%d): err mismatch %v vs %v",
						seq, op, at, realErr, naiveErr)
				}
			case 1:
				realOK, realErr := real.TryRetry(at)
				naiveOK, k, cost, bal, naiveErr := naive.tryRetry(at)
				t.Logf("seq=%d op=%d TryRetry(%d) -> allowed=%v err=%v "+
					"(basis: k=%d cost=%d balance=%d; naive allowed=%v err=%v)",
					seq, op, at, realOK, realErr, k, cost, bal, naiveOK, naiveErr)
				if realOK != naiveOK || (realErr == nil) != (naiveErr == nil) {
					t.Fatalf("seq %d op %d TryRetry(%d): got (%v,%v), naive (%v,%v)",
						seq, op, at, realOK, realErr, naiveOK, naiveErr)
				}
				if realErr == nil {
					checkRetryInvariants(t, real, c, mx, cm, r, d, realOK)
				}
			default:
				realBal, realErr := real.Balance(at)
				naiveBal, naiveErr := naive.balanceOp(at)
				t.Logf("seq=%d op=%d Balance(%d) -> %d err=%v (naive %d err=%v)",
					seq, op, at, realBal, realErr, naiveBal, naiveErr)
				if realBal != naiveBal || (realErr == nil) != (naiveErr == nil) {
					t.Fatalf("seq %d op %d Balance(%d): got (%d,%v), naive (%d,%v)",
						seq, op, at, realBal, realErr, naiveBal, naiveErr)
				}
			}
			if at >= 0 && at <= maxTime && at >= now {
				now = at
			}
		}
		// Final cross-check of the internal aggregates against the naive scan.
		validDeps, validWds, costSum := naive.stats(now)
		if real.validDeposits != validDeps ||
			real.validWithdrawals != validWds ||
			real.withdrawCostSum != costSum {
			t.Fatalf("seq %d: aggregates drifted: got (%d,%d,%d), naive (%d,%d,%d)",
				seq, real.validDeposits, real.validWithdrawals, real.withdrawCostSum,
				validDeps, validWds, costSum)
		}
	}
}

// checkRetryInvariants verifies, right after an accepted TryRetry, that
// every valid withdrawal's frozen cost lies in [C, C*Mx] and that an
// allowed retry leaves the frozen-cost sum within the budget ceiling.
func checkRetryInvariants(t *testing.T, b *Budget, c, mx, cm, r, d int64, allowed bool) {
	t.Helper()
	for _, w := range b.withdrawals[b.wdHead:] {
		if w.cost < c || w.cost > c*mx {
			t.Fatalf("frozen cost %d outside [%d, %d]", w.cost, c, c*mx)
		}
	}
	if allowed {
		dep := b.validDeposits
		if dep > cm {
			dep = cm
		}
		if b.withdrawCostSum > r+d*dep {
			t.Fatalf("frozen cost sum %d exceeds ceiling %d", b.withdrawCostSum, r+d*dep)
		}
	}
}

// TestAmortizedCleanup proves, at 1000 and 100000 operations, that the
// total number of cleaned events never exceeds the number of registered
// events and that the total number of examined events stays within a
// constant multiple of the operation count — i.e. cleanup is amortized
// O(1) per operation and no operation rescans the ledgers.
func TestAmortizedCleanup(t *testing.T) {
	for _, n := range []int{1_000, 100_000} {
		b := mustNew(t, 7, 11, 1, 1, 5, 3, 10)
		rng := rand.New(rand.NewSource(int64(n)))
		now := int64(0)
		var registered, ops int64
		for i := 0; i < n; i++ {
			now += rng.Int63n(3)
			switch rng.Intn(3) {
			case 0:
				if err := b.Request(now); err != nil {
					t.Fatalf("n=%d op=%d Request: %v", n, i, err)
				}
				registered++
			case 1:
				ok, err := b.TryRetry(now)
				if err != nil {
					t.Fatalf("n=%d op=%d TryRetry: %v", n, i, err)
				}
				if ok {
					registered++
				}
			default:
				if _, err := b.Balance(now); err != nil {
					t.Fatalf("n=%d op=%d Balance: %v", n, i, err)
				}
			}
			ops++
		}
		t.Logf("n=%d: registered=%d cleaned=%d examined=%d ops=%d",
			n, registered, b.cleanedEvents, b.examinedEvents, ops)
		if b.cleanedEvents > registered {
			t.Fatalf("n=%d: cleaned %d events but only %d were registered",
				n, b.cleanedEvents, registered)
		}
		// Each eviction run examines at most one event it does not remove
		// per ledger (two ledgers), so examined <= cleaned + 2*ops <= 3*ops:
		// a constant multiple of the operation count.
		if b.examinedEvents > b.cleanedEvents+2*ops {
			t.Fatalf("n=%d: examined %d exceeds cleaned %d + 2*ops %d",
				n, b.examinedEvents, b.cleanedEvents, 2*ops)
		}
		if b.examinedEvents > 3*ops {
			t.Fatalf("n=%d: examined %d exceeds 3*ops %d", n, b.examinedEvents, 3*ops)
		}
	}
}

// TestDeterministicReplay: the same operation sequence replayed on two
// fresh budgets yields identical return values and balance sequences.
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	wd, wc, d, c, r, mx, cm := randomParams(rng, 1)
	type op struct {
		kind int
		at   int64
	}
	var seq []op
	now := int64(0)
	for i := 0; i < 500; i++ {
		kind, at := randomOp(rng, now)
		seq = append(seq, op{kind, at})
		if at >= 0 && at <= maxTime && at >= now {
			now = at
		}
	}

	run := func() []any {
		b, err := New(wd, wc, d, c, r, mx, cm)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		var out []any
		for _, o := range seq {
			switch o.kind {
			case 0:
				out = append(out, b.Request(o.at) == nil)
			case 1:
				ok, err := b.TryRetry(o.at)
				out = append(out, ok, err == nil)
			default:
				bal, err := b.Balance(o.at)
				out = append(out, bal, err == nil)
			}
		}
		return out
	}

	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("replay length mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay diverged at output %d: %v vs %v", i, first[i], second[i])
		}
	}
}

// TestConcurrent: concurrent calls must be race-free and equivalent to some
// serial order. Timestamps come from an atomic counter so most calls are
// accepted; afterwards the invariants must still hold.
func TestConcurrent(t *testing.T) {
	b := mustNew(t, 50, 50, 1, 1, 100, 3, 20)
	const workers = 8
	const perWorker = 250
	var wg sync.WaitGroup
	var counter atomic.Int64
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < perWorker; i++ {
				now := counter.Add(1) - 1
				switch rng.Intn(3) {
				case 0:
					_ = b.Request(now)
				case 1:
					_, _ = b.TryRetry(now)
				default:
					_, _ = b.Balance(now)
				}
			}
		}(int64(w))
	}
	wg.Wait()

	// Whatever serial order happened, the recorded frozen costs must each
	// lie in [C, C*Mx] and the aggregates must match the surviving events.
	for _, wdEvent := range b.withdrawals[b.wdHead:] {
		if wdEvent.cost < b.c || wdEvent.cost > b.c*b.mx {
			t.Fatalf("frozen cost %d outside [%d, %d]", wdEvent.cost, b.c, b.c*b.mx)
		}
	}
	var sum int64
	for _, wdEvent := range b.withdrawals[b.wdHead:] {
		sum += wdEvent.cost
	}
	if sum != b.withdrawCostSum {
		t.Fatalf("withdrawCostSum %d != actual sum %d", b.withdrawCostSum, sum)
	}
	if int64(len(b.deposits)-b.depHead) != b.validDeposits {
		t.Fatalf("validDeposits %d != ledger count %d",
			b.validDeposits, len(b.deposits)-b.depHead)
	}
}
