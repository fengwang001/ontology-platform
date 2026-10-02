package checkpoint

import (
	"errors"
	"sync"
	"testing"
)

func mustNew(t *testing.T, intervalMs, walBudget, targetPermille int64) *Scheduler {
	t.Helper()
	s, err := NewScheduler(intervalMs, walBudget, targetPermille)
	if err != nil {
		t.Fatalf("NewScheduler(%d, %d, %d): %v", intervalMs, walBudget, targetPermille, err)
	}
	return s
}

func mustBegin(t *testing.T, s *Scheduler, now, walPos, dirty int64) {
	t.Helper()
	if err := s.Begin(now, walPos, dirty); err != nil {
		t.Fatalf("Begin(%d, %d, %d): %v", now, walPos, dirty, err)
	}
}

func mustTick(t *testing.T, s *Scheduler, now, walPos int64) (int64, int64) {
	t.Helper()
	q, need, err := s.Tick(now, walPos)
	if err != nil {
		t.Fatalf("Tick(%d, %d): %v", now, walPos, err)
	}
	return q, need
}

func mustWrote(t *testing.T, s *Scheduler, k int64) {
	t.Helper()
	if err := s.Wrote(k); err != nil {
		t.Fatalf("Wrote(%d): %v", k, err)
	}
}

func TestConstructorErrors(t *testing.T) {
	cases := []struct {
		name                          string
		interval, walBudget, permille int64
		want                          error
	}{
		{"zero interval", 0, 100, 500, ErrNonPositiveInterval},
		{"negative interval", -1, 100, 500, ErrNonPositiveInterval},
		{"zero budget", 1000, 0, 500, ErrNonPositiveBudget},
		{"negative budget", 1000, -1, 500, ErrNonPositiveBudget},
		{"zero permille", 1000, 100, 0, ErrInvalidTargetFraction},
		{"permille 1000", 1000, 100, 1000, ErrInvalidTargetFraction},
		{"negative permille", 1000, 100, -3, ErrInvalidTargetFraction},
		{"interval reported first", 0, 0, 0, ErrNonPositiveInterval},
		{"budget reported before permille", 1000, -1, 0, ErrNonPositiveBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewScheduler(tc.interval, tc.walBudget, tc.permille); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := NewScheduler(1, 1, 1); err != nil {
		t.Fatalf("boundary permille 1: %v", err)
	}
	if _, err := NewScheduler(1, 1, 999); err != nil {
		t.Fatalf("boundary permille 999: %v", err)
	}
}

// When e == T*F/1000 exactly, q1 == N; one millisecond earlier yields the
// floored value below N.
func TestTimeQuotaExactBoundary(t *testing.T) {
	// T*F/1000 = 1000*500/1000 = 500 ms. WAL budget is huge so q2 stays 0.
	s := mustNew(t, 1000, 1<<60, 500)
	mustBegin(t, s, 0, 0, 100)
	if q, need := mustTick(t, s, 500, 0); q != 100 || need != 100 {
		t.Fatalf("e == T*F/1000: got Q=%d need=%d, want Q=100 need=100", q, need)
	}

	s2 := mustNew(t, 1000, 1<<60, 500)
	mustBegin(t, s2, 0, 0, 100)
	// floor(100*499*1000/(1000*500)) = floor(99.8) = 99.
	if q, need := mustTick(t, s2, 499, 0); q != 99 || need != 99 {
		t.Fatalf("e == T*F/1000 - 1: got Q=%d need=%d, want Q=99 need=99", q, need)
	}
}

// When WAL progress outruns time progress, the log quota wins.
func TestLogQuotaDominates(t *testing.T) {
	// q1 = floor(1000*1*1000/(1_000_000*500)) = 0.
	// q2 = floor(1000*100*1000/(1000*500)) = 200.
	s := mustNew(t, 1_000_000, 1000, 500)
	mustBegin(t, s, 0, 0, 1000)
	if q, need := mustTick(t, s, 1, 100); q != 200 || need != 200 {
		t.Fatalf("got Q=%d need=%d, want Q=200 need=200 (log quota)", q, need)
	}
}

// When time progress outruns WAL progress, the time quota wins.
func TestTimeQuotaDominates(t *testing.T) {
	// q1 = floor(1000*100*1000/(1000*500)) = 200.
	// q2 = floor(1000*1*1000/(1_000_000*500)) = 0.
	s := mustNew(t, 1000, 1_000_000, 500)
	mustBegin(t, s, 0, 0, 1000)
	if q, need := mustTick(t, s, 100, 1); q != 200 || need != 200 {
		t.Fatalf("got Q=%d need=%d, want Q=200 need=200 (time quota)", q, need)
	}
}

// Each progress quota is capped at N on its own.
func TestQuotasCappedAtN(t *testing.T) {
	// Huge elapsed time, no WAL: q1 capped at N, q2 = 0.
	s := mustNew(t, 1, 1<<60, 1)
	mustBegin(t, s, 0, 0, 50)
	if q, _ := mustTick(t, s, 1<<40, 0); q != 50 {
		t.Fatalf("time cap: got Q=%d, want 50", q)
	}

	// Huge WAL, no elapsed time: q2 capped at N, q1 = 0.
	s2 := mustNew(t, 1<<60, 1, 1)
	mustBegin(t, s2, 0, 0, 50)
	if q, _ := mustTick(t, s2, 0, 1<<40); q != 50 {
		t.Fatalf("log cap: got Q=%d, want 50", q)
	}
}

// N == 0 completes immediately without entering the in-progress state.
func TestZeroDirtyCompletesImmediately(t *testing.T) {
	s := mustNew(t, 1000, 1000, 500)
	mustBegin(t, s, 10, 20, 0)
	active, total, written, completed := s.Status()
	if active || total != 0 || written != 0 || completed != 1 {
		t.Fatalf("Status = (%v, %d, %d, %d), want (false, 0, 0, 1)", active, total, written, completed)
	}
	if _, _, err := s.Tick(10, 20); !errors.Is(err, ErrNoCheckpointInProgress) {
		t.Fatalf("Tick after zero-dirty Begin: got %v, want %v", err, ErrNoCheckpointInProgress)
	}
	// No minimum interval between checkpoints: a new one may begin at the
	// same (now, walPos).
	mustBegin(t, s, 10, 20, 7)
	if active, total, _, _ := s.Status(); !active || total != 7 {
		t.Fatalf("follow-up Begin: active=%v total=%d, want active=true total=7", active, total)
	}
}

// N and e at 2^40 with T = F = 1 must not overflow.
func TestHugeValuesNoOverflow(t *testing.T) {
	const big40 = int64(1) << 40
	s := mustNew(t, 1, 1, 1)
	mustBegin(t, s, 0, 0, big40)
	if q, need := mustTick(t, s, big40, big40); q != big40 || need != big40 {
		t.Fatalf("got Q=%d need=%d, want Q=%d need=%d (capped at N)", q, need, big40, big40)
	}

	// Exact floor without capping: N = 2^40, T = 2^40, F = 1, e = 2^30.
	// q1 = floor(2^40 * 2^30 * 1000 / 2^40) = 2^30 * 1000 = 1073741824000.
	// The intermediate product is ~2^80, far beyond int64.
	s2 := mustNew(t, big40, 1, 1)
	mustBegin(t, s2, 0, 0, big40)
	const want = int64(1) << 30 * 1000
	if q, _ := mustTick(t, s2, 1<<30, 0); q != want {
		t.Fatalf("got Q=%d, want %d", q, want)
	}
}

func TestWroteErrors(t *testing.T) {
	s := mustNew(t, 1000, 1000, 500)
	// Idle: no-checkpoint reported before k validation.
	if err := s.Wrote(0); !errors.Is(err, ErrNoCheckpointInProgress) {
		t.Fatalf("idle Wrote(0): got %v, want %v", err, ErrNoCheckpointInProgress)
	}
	mustBegin(t, s, 0, 0, 10)
	for _, k := range []int64{0, -1, -100} {
		if err := s.Wrote(k); !errors.Is(err, ErrNonPositiveWrite) {
			t.Fatalf("Wrote(%d): got %v, want %v", k, err, ErrNonPositiveWrite)
		}
	}
	if err := s.Wrote(11); !errors.Is(err, ErrWriteExceedsRemaining) {
		t.Fatalf("Wrote(11) of 10: got %v, want %v", err, ErrWriteExceedsRemaining)
	}
	mustWrote(t, s, 6)
	if err := s.Wrote(5); !errors.Is(err, ErrWriteExceedsRemaining) {
		t.Fatalf("Wrote(5) with 4 left: got %v, want %v", err, ErrWriteExceedsRemaining)
	}
	mustWrote(t, s, 4)
	if active, _, _, completed := s.Status(); active || completed != 1 {
		t.Fatalf("after final Wrote: active=%v completed=%d, want false, 1", active, completed)
	}
	if err := s.Wrote(1); !errors.Is(err, ErrNoCheckpointInProgress) {
		t.Fatalf("Wrote after completion: got %v, want %v", err, ErrNoCheckpointInProgress)
	}
}

func TestBeginValidationOrder(t *testing.T) {
	s := mustNew(t, 1000, 1000, 500)
	mustBegin(t, s, 100, 100, 10)
	// Negativity is reported before the in-progress state.
	if err := s.Begin(-1, -1, -1); !errors.Is(err, ErrNegativeNow) {
		t.Fatalf("got %v, want %v", err, ErrNegativeNow)
	}
	if err := s.Begin(0, -1, -1); !errors.Is(err, ErrNegativeWalPos) {
		t.Fatalf("got %v, want %v", err, ErrNegativeWalPos)
	}
	if err := s.Begin(0, 0, -1); !errors.Is(err, ErrNegativeDirty) {
		t.Fatalf("got %v, want %v", err, ErrNegativeDirty)
	}
	// In-progress is reported before regression.
	if err := s.Begin(50, 50, 5); !errors.Is(err, ErrCheckpointInProgress) {
		t.Fatalf("got %v, want %v", err, ErrCheckpointInProgress)
	}
	mustWrote(t, s, 10)
	// Regression: now checked before walPos.
	if err := s.Begin(99, 99, 5); !errors.Is(err, ErrNowRegression) {
		t.Fatalf("got %v, want %v", err, ErrNowRegression)
	}
	if err := s.Begin(100, 99, 5); !errors.Is(err, ErrWalPosRegression) {
		t.Fatalf("got %v, want %v", err, ErrWalPosRegression)
	}
}

func TestTickValidationOrder(t *testing.T) {
	s := mustNew(t, 1000, 1000, 500)
	// Negativity is reported before the no-checkpoint state.
	if _, _, err := s.Tick(-1, -1); !errors.Is(err, ErrNegativeNow) {
		t.Fatalf("got %v, want %v", err, ErrNegativeNow)
	}
	if _, _, err := s.Tick(0, -1); !errors.Is(err, ErrNegativeWalPos) {
		t.Fatalf("got %v, want %v", err, ErrNegativeWalPos)
	}
	// No-checkpoint is reported before regression.
	if _, _, err := s.Tick(0, 0); !errors.Is(err, ErrNoCheckpointInProgress) {
		t.Fatalf("got %v, want %v", err, ErrNoCheckpointInProgress)
	}
	mustBegin(t, s, 100, 100, 10)
	if _, _, err := s.Tick(99, 99); !errors.Is(err, ErrNowRegression) {
		t.Fatalf("got %v, want %v", err, ErrNowRegression)
	}
	if _, _, err := s.Tick(100, 99); !errors.Is(err, ErrWalPosRegression) {
		t.Fatalf("got %v, want %v", err, ErrWalPosRegression)
	}
}

// A rejected operation must not change any state, including the recorded
// last-seen now and walPos.
func TestRejectedOpsKeepState(t *testing.T) {
	s := mustNew(t, 1000, 1000, 500)
	mustBegin(t, s, 100, 100, 10)
	q0, need0 := mustTick(t, s, 200, 300)
	st0 := []int64{}
	{
		_, total, written, completed := s.Status()
		st0 = []int64{total, written, completed}
	}

	// Rejected Tick: now regression.
	if _, _, err := s.Tick(150, 400); !errors.Is(err, ErrNowRegression) {
		t.Fatalf("got %v, want %v", err, ErrNowRegression)
	}
	// Rejected Tick: walPos regression.
	if _, _, err := s.Tick(200, 250); !errors.Is(err, ErrWalPosRegression) {
		t.Fatalf("got %v, want %v", err, ErrWalPosRegression)
	}
	// Rejected Wrote: exceeds remaining.
	if err := s.Wrote(11); !errors.Is(err, ErrWriteExceedsRemaining) {
		t.Fatalf("got %v, want %v", err, ErrWriteExceedsRemaining)
	}

	// State unchanged, and the last accepted (now, walPos) is still
	// (200, 300): replaying the same Tick succeeds with the same result.
	if q, need := mustTick(t, s, 200, 300); q != q0 || need != need0 {
		t.Fatalf("replayed Tick = (%d, %d), want (%d, %d)", q, need, q0, need0)
	}
	if _, total, written, completed := s.Status(); total != st0[0] || written != st0[1] || completed != st0[2] {
		t.Fatalf("Status changed after rejected ops: (%d, %d, %d), want %v", total, written, completed, st0)
	}

	// Rejected Begin must not update last-seen values either.
	mustWrote(t, s, 10)
	if err := s.Begin(150, 100, 5); !errors.Is(err, ErrNowRegression) {
		t.Fatalf("got %v, want %v", err, ErrNowRegression)
	}
	mustBegin(t, s, 200, 300, 5)
	if active, total, _, completed := s.Status(); !active || total != 5 || completed != 1 {
		t.Fatalf("Status = (active=%v, N=%d, completed=%d), want (true, 5, 1)", active, total, completed)
	}
}

// Within one checkpoint, accepted Ticks with non-decreasing inputs yield a
// non-decreasing cumulative quota.
func TestTickQuotaMonotonic(t *testing.T) {
	s := mustNew(t, 997, 991, 333)
	mustBegin(t, s, 0, 0, 100000)
	prev := int64(-1)
	var now, wal int64
	for i := 0; i < 500; i++ {
		now += int64(i % 7)
		wal += int64(i % 5)
		q, _ := mustTick(t, s, now, wal)
		if q < prev {
			t.Fatalf("Q regressed: %d -> %d at (now=%d, wal=%d)", prev, q, now, wal)
		}
		prev = q
	}
}

// Concurrent Wrote/Tick/Status calls behave as some serial order: exactly N
// writes succeed, the checkpoint completes exactly once, and the written
// count never exceeds N.
func TestConcurrentAccess(t *testing.T) {
	const n = 10000
	s := mustNew(t, 1000, 1000, 500)
	mustBegin(t, s, 0, 0, n)

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n/4; i++ {
				if err := s.Wrote(1); err == nil {
					mu.Lock()
					succeeded++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for now := int64(0); now <= 2000; now++ {
			q, need, err := s.Tick(now, now)
			if err == nil {
				if q < 0 || q > n || need < 0 || need > n {
					t.Errorf("Tick(%d): invalid (Q=%d, need=%d)", now, q, need)
				}
			}
			s.Status()
		}
	}()
	wg.Wait()

	if succeeded != n {
		t.Fatalf("successful Wrote calls = %d, want %d", succeeded, n)
	}
	if active, total, written, completed := s.Status(); active || total != 0 || written != 0 || completed != 1 {
		t.Fatalf("Status = (%v, %d, %d, %d), want (false, 0, 0, 1)", active, total, written, completed)
	}
}
