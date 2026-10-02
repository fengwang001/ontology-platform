package checkpoint

import (
	"errors"
	"math/big"
	"math/rand"
	"testing"
)

// naiveScheduler is a deliberately simple reference implementation of the
// specification, written with big.Rat arithmetic. The randomized test below
// replays identical operation sequences against both implementations and
// requires identical results and identical rejection reasons.
type naiveScheduler struct {
	interval, walBudget, permille int64

	active             bool
	startNow, startWal int64
	total, written     int64
	completed          int64
	lastNow, lastWal   int64
	lastQ1, lastQ2     int64
}

func newNaive(interval, walBudget, permille int64) *naiveScheduler {
	return &naiveScheduler{interval: interval, walBudget: walBudget, permille: permille}
}

// ratQuota computes min(n, floor(n*progress*1000/(unit*f))) with big.Rat.
func ratQuota(n, progress, unit, f int64) int64 {
	if n <= 0 || progress <= 0 {
		return 0
	}
	r := new(big.Rat).SetInt64(n)
	r.Mul(r, new(big.Rat).SetInt64(progress))
	r.Mul(r, new(big.Rat).SetInt64(1000))
	den := new(big.Rat).SetInt64(unit)
	den.Mul(den, new(big.Rat).SetInt64(f))
	r.Quo(r, den)
	// Floor of a non-negative rational: big.Int.Quo truncates toward zero,
	// which is the floor for non-negative values.
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if !q.IsInt64() || q.Int64() > n {
		return n
	}
	return q.Int64()
}

func (n *naiveScheduler) Begin(now, walPos, dirty int64) error {
	if now < 0 {
		return ErrNegativeNow
	}
	if walPos < 0 {
		return ErrNegativeWalPos
	}
	if dirty < 0 {
		return ErrNegativeDirty
	}
	if n.active {
		return ErrCheckpointInProgress
	}
	if now < n.lastNow {
		return ErrNowRegression
	}
	if walPos < n.lastWal {
		return ErrWalPosRegression
	}
	n.lastNow = now
	n.lastWal = walPos
	if dirty == 0 {
		n.completed++
		return nil
	}
	n.active = true
	n.startNow = now
	n.startWal = walPos
	n.total = dirty
	n.written = 0
	return nil
}

func (n *naiveScheduler) Tick(now, walPos int64) (int64, int64, error) {
	if now < 0 {
		return 0, 0, ErrNegativeNow
	}
	if walPos < 0 {
		return 0, 0, ErrNegativeWalPos
	}
	if !n.active {
		return 0, 0, ErrNoCheckpointInProgress
	}
	if now < n.lastNow {
		return 0, 0, ErrNowRegression
	}
	if walPos < n.lastWal {
		return 0, 0, ErrWalPosRegression
	}
	n.lastNow = now
	n.lastWal = walPos
	n.lastQ1 = ratQuota(n.total, now-n.startNow, n.interval, n.permille)
	n.lastQ2 = ratQuota(n.total, walPos-n.startWal, n.walBudget, n.permille)
	q := n.lastQ1
	if n.lastQ2 > q {
		q = n.lastQ2
	}
	need := q - n.written
	if need < 0 {
		need = 0
	}
	return q, need, nil
}

func (n *naiveScheduler) Wrote(k int64) error {
	if !n.active {
		return ErrNoCheckpointInProgress
	}
	if k <= 0 {
		return ErrNonPositiveWrite
	}
	if k > n.total-n.written {
		return ErrWriteExceedsRemaining
	}
	n.written += k
	if n.written >= n.total {
		n.active = false
		n.total = 0
		n.written = 0
		n.completed++
	}
	return nil
}

func (n *naiveScheduler) Status() (bool, int64, int64, int64) {
	return n.active, n.total, n.written, n.completed
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) && errors.Is(b, a)
}

// TestAgainstNaiveModel replays 2000 random operation sequences against both
// the real scheduler and the big.Rat reference model, requiring identical
// outputs and identical rejection reasons. Every operation is logged with
// its inputs, outputs, and the decision basis (q1/q2 for quotas, the
// validation rule for rejections).
func TestAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for seq := 0; seq < 2000; seq++ {
		interval := 1 + rng.Int63n(1<<20)
		walBudget := 1 + rng.Int63n(1<<20)
		permille := int64(1 + rng.Intn(999))
		if rng.Intn(8) == 0 {
			// Occasionally stress huge magnitudes near 2^40.
			interval = 1 + rng.Int63n(1<<40)
			walBudget = 1 + rng.Int63n(1<<40)
		}
		s, err := NewScheduler(interval, walBudget, permille)
		if err != nil {
			t.Fatalf("seq %d: NewScheduler: %v", seq, err)
		}
		n := newNaive(interval, walBudget, permille)

		var now, wal int64
		var lastQ int64 = -1
		ops := 5 + rng.Intn(25)

		genNow := func() int64 {
			switch rng.Intn(12) {
			case 0:
				return -1 - rng.Int63n(1000) // negative
			case 1:
				return now - 1 - rng.Int63n(1000) // regression
			case 2:
				return now // unchanged
			default:
				delta := rng.Int63n(1 << 20)
				if rng.Intn(16) == 0 {
					delta = rng.Int63n(1 << 40)
				}
				return now + delta
			}
		}
		genWal := func() int64 {
			switch rng.Intn(12) {
			case 0:
				return -1 - rng.Int63n(1000)
			case 1:
				return wal - 1 - rng.Int63n(1000)
			case 2:
				return wal
			default:
				delta := rng.Int63n(1 << 20)
				if rng.Intn(16) == 0 {
					delta = rng.Int63n(1 << 40)
				}
				return wal + delta
			}
		}
		genDirty := func() int64 {
			switch rng.Intn(8) {
			case 0:
				return 0
			case 1:
				return -1 - rng.Int63n(100)
			case 2:
				return 1 + rng.Int63n(1<<40)
			default:
				return 1 + rng.Int63n(10000)
			}
		}
		genK := func() int64 {
			var remaining int64
			if n.active {
				remaining = n.total - n.written
			}
			switch rng.Intn(8) {
			case 0:
				return 0
			case 1:
				return -1 - rng.Int63n(10)
			case 2:
				return remaining + 1 + rng.Int63n(100) // exceeds remaining
			case 3:
				if remaining > 0 {
					return remaining // finish exactly
				}
				return 1
			default:
				hi := remaining + 2
				if hi < 1 {
					hi = 1
				}
				return 1 + rng.Int63n(hi)
			}
		}

		for op := 0; op < ops; op++ {
			switch rng.Intn(4) {
			case 0: // Begin
				aNow, aWal, dirty := genNow(), genWal(), genDirty()
				errA := s.Begin(aNow, aWal, dirty)
				errB := n.Begin(aNow, aWal, dirty)
				t.Logf("seq=%d op=%d Begin(now=%d, walPos=%d, dirty=%d) -> err=%v",
					seq, op, aNow, aWal, dirty, errA)
				if !sameErr(errA, errB) {
					t.Fatalf("seq %d op %d Begin(%d, %d, %d): scheduler err=%v, naive err=%v",
						seq, op, aNow, aWal, dirty, errA, errB)
				}
				if errA == nil {
					now, wal = aNow, aWal
					lastQ = -1
				}
			case 1: // Tick
				aNow, aWal := genNow(), genWal()
				qA, needA, errA := s.Tick(aNow, aWal)
				qB, needB, errB := n.Tick(aNow, aWal)
				t.Logf("seq=%d op=%d Tick(now=%d, walPos=%d) -> Q=%d need=%d err=%v (basis: q1=%d q2=%d written=%d)",
					seq, op, aNow, aWal, qA, needA, errA, n.lastQ1, n.lastQ2, n.written)
				if !sameErr(errA, errB) || qA != qB || needA != needB {
					t.Fatalf("seq %d op %d Tick(%d, %d): scheduler=(%d, %d, %v), naive=(%d, %d, %v)",
						seq, op, aNow, aWal, qA, needA, errA, qB, needB, errB)
				}
				if errA == nil {
					now, wal = aNow, aWal
					if lastQ >= 0 && qA < lastQ {
						t.Fatalf("seq %d op %d: quota regressed %d -> %d", seq, op, lastQ, qA)
					}
					lastQ = qA
				}
			case 2: // Wrote
				k := genK()
				errA := s.Wrote(k)
				errB := n.Wrote(k)
				t.Logf("seq=%d op=%d Wrote(k=%d) -> err=%v (basis: remaining=%d)",
					seq, op, k, errA, func() int64 {
						if n.active {
							return n.total - n.written
						}
						return 0
					}())
				if !sameErr(errA, errB) {
					t.Fatalf("seq %d op %d Wrote(%d): scheduler err=%v, naive err=%v",
						seq, op, k, errA, errB)
				}
				if errA == nil && !n.active {
					lastQ = -1
				}
			case 3: // Status
				activeA, totalA, writtenA, completedA := s.Status()
				activeB, totalB, writtenB, completedB := n.Status()
				t.Logf("seq=%d op=%d Status() -> (active=%v, N=%d, written=%d, completed=%d)",
					seq, op, activeA, totalA, writtenA, completedA)
				if activeA != activeB || totalA != totalB || writtenA != writtenB || completedA != completedB {
					t.Fatalf("seq %d op %d Status: scheduler=(%v, %d, %d, %d), naive=(%v, %d, %d, %d)",
						seq, op, activeA, totalA, writtenA, completedA,
						activeB, totalB, writtenB, completedB)
				}
				if activeA && writtenA > totalA {
					t.Fatalf("seq %d op %d: written %d exceeds N %d", seq, op, writtenA, totalA)
				}
			}
		}
	}
}

// TestDeterministicReplay runs one fixed operation sequence twice and
// requires identical results and errors.
func TestDeterministicReplay(t *testing.T) {
	type result struct {
		q, need int64
		err     error
	}
	run := func() []result {
		s := mustNew(t, 1000, 4096, 500)
		var out []result
		record := func(q, need int64, err error) { out = append(out, result{q, need, err}) }
		steps := []func(){
			func() { record(0, 0, s.Begin(0, 0, 100)) },
			func() { q, need, err := s.Tick(100, 100); record(q, need, err) },
			func() { record(0, 0, s.Wrote(10)) },
			func() { q, need, err := s.Tick(100, 99); record(q, need, err) },
			func() { q, need, err := s.Tick(100, 100); record(q, need, err) },
			func() { record(0, 0, s.Wrote(90)) },
			func() { record(0, 0, s.Begin(50, 50, 0)) },
			func() { record(0, 0, s.Begin(100, 100, 0)) },
			func() { record(0, 0, s.Begin(100, 100, 7)) },
			func() { q, need, err := s.Tick(2000, 1<<40); record(q, need, err) },
			func() { record(0, 0, s.Wrote(8)) },
			func() { record(0, 0, s.Wrote(7)) },
		}
		for _, step := range steps {
			step()
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("result count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		a, b := first[i], second[i]
		if a.q != b.q || a.need != b.need || !sameErr(a.err, b.err) {
			t.Fatalf("step %d differs: (%d, %d, %v) vs (%d, %d, %v)", i, a.q, a.need, a.err, b.q, b.need, b.err)
		}
	}
}
