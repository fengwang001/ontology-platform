package retry

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recorder 记录注入 sleep 被调用的间隔。
type recorder struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (rec *recorder) sleep(d time.Duration) {
	rec.mu.Lock()
	rec.sleeps = append(rec.sleeps, d)
	rec.mu.Unlock()
}

func (rec *recorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.sleeps)
}

// 语义 1：n 次尝试只等 n-1 次，最后一次失败后绝不再等。
func TestDelaysOneLessThanAttempts(t *testing.T) {
	rec := &recorder{}
	r := New(Policy{MaxAttempts: 3, Base: time.Millisecond}, rec.sleep, nil)
	boom := errors.New("boom")
	n, err := r.Do(func(int) error { return boom })
	if n != 3 || !errors.Is(err, ErrExhausted) {
		t.Fatalf("Do = (%d, %v), want (3, ErrExhausted)", n, err)
	}
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("len(Delays()) = %d, want 2", got)
	}
	if rec.count() != 2 {
		t.Fatalf("sleep called %d times, want 2 (no wait after last failure)", rec.count())
	}
}

// 语义 4：Permanent 错误立即中止，不再等也不再尝试。
func TestPermanentAbortsImmediately(t *testing.T) {
	rec := &recorder{}
	r := New(Policy{MaxAttempts: 5, Base: time.Millisecond}, rec.sleep, nil)
	orig := errors.New("disk gone")
	calls := 0
	n, err := r.Do(func(attempt int) error {
		calls++
		if attempt == 2 {
			return Permanent(orig)
		}
		return errors.New("transient")
	})
	if n != 2 || calls != 2 {
		t.Fatalf("attempts = %d, calls = %d, want 2/2", n, calls)
	}
	if !errors.Is(err, ErrAborted) || !errors.Is(err, orig) {
		t.Fatalf("err = %v, want Is(ErrAborted) && Is(orig)", err)
	}
	if rec.count() != 1 || len(r.Delays()) != 1 {
		t.Fatalf("waits = %d, delays = %d, want 1/1", rec.count(), len(r.Delays()))
	}
}

// 语义 5：第 k 次成功即止，返回 k，Delays 长度为 k-1。
func TestSuccessStops(t *testing.T) {
	rec := &recorder{}
	r := New(Policy{MaxAttempts: 5, Base: time.Millisecond}, rec.sleep, nil)
	calls := 0
	n, err := r.Do(func(int) error {
		calls++
		if calls == 3 {
			return nil
		}
		return errors.New("flaky")
	})
	if err != nil || n != 3 || calls != 3 {
		t.Fatalf("Do = (%d, %v), calls = %d, want (3, nil), calls 3", n, err, calls)
	}
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("len(Delays()) = %d, want 2", got)
	}
}

// 语义 6：次数用尽时能取到最后一次的原错误，而非第一次。
func TestExhaustedReturnsLastError(t *testing.T) {
	first := errors.New("first")
	last := errors.New("last")
	r := New(Policy{MaxAttempts: 3}, func(time.Duration) {}, nil)
	_, err := r.Do(func(attempt int) error {
		if attempt == 1 {
			return first
		}
		return last
	})
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("err = %v, want Is(ErrExhausted)", err)
	}
	if !errors.Is(err, last) {
		t.Fatalf("err = %v, want Is(last)", err)
	}
	if errors.Is(err, first) {
		t.Fatalf("err = %v, must not wrap first error", err)
	}
}

// 语义 7：attempt 从 1 连续递增；MaxAttempts<=0 时至少跑一次。
func TestAttemptNumbering(t *testing.T) {
	var got []int
	r := New(Policy{MaxAttempts: 4}, func(time.Duration) {}, nil)
	r.Do(func(attempt int) error {
		got = append(got, attempt)
		return errors.New("x")
	})
	want := []int{1, 2, 3, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempts = %v, want %v", got, want)
		}
	}
	got = got[:0]
	r0 := New(Policy{MaxAttempts: 0}, func(time.Duration) {}, nil)
	n, _ := r0.Do(func(attempt int) error {
		got = append(got, attempt)
		return errors.New("x")
	})
	if n != 1 || len(got) != 1 || got[0] != 1 {
		t.Fatalf("MaxAttempts=0: n=%d attempts=%v, want single attempt 1", n, got)
	}
}

// 语义 8（串行复用）：第二次 Do 的 Delays 不累积上一次的。
func TestRunnerReuseResetsDelays(t *testing.T) {
	r := New(Policy{MaxAttempts: 3, Base: time.Millisecond}, func(time.Duration) {}, nil)
	r.Do(func(int) error { return errors.New("x") })
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("first run: len(Delays()) = %d, want 2", got)
	}
	n, err := r.Do(func(int) error { return nil })
	if n != 1 || err != nil {
		t.Fatalf("second Do = (%d, %v), want (1, nil)", n, err)
	}
	if got := len(r.Delays()); got != 0 {
		t.Fatalf("second run: len(Delays()) = %d, want 0", got)
	}
}

// 语义 8（并发）：多 goroutine 共享 Runner，-race 干净且不串台。
func TestConcurrentDo(t *testing.T) {
	const goroutines = 8
	const runsEach = 5
	var slept atomic.Int64
	var sleptSum atomic.Int64
	sleep := func(d time.Duration) {
		slept.Add(1)
		sleptSum.Add(int64(d))
	}
	r := New(Policy{MaxAttempts: 3, Base: time.Millisecond, Factor: 2}, sleep, nil)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < runsEach; i++ {
				n, err := r.Do(func(int) error { return errors.New("x") })
				if n != 3 || !errors.Is(err, ErrExhausted) {
					t.Errorf("Do = (%d, %v), want (3, ErrExhausted)", n, err)
				}
			}
		}()
	}
	wg.Wait()
	total := int64(goroutines * runsEach)
	if got := slept.Load(); got != 2*total {
		t.Fatalf("sleeps = %d, want %d (2 per run, no cross-talk)", got, 2*total)
	}
	wantSum := total * int64(time.Millisecond+2*time.Millisecond)
	if got := sleptSum.Load(); got != wantSum {
		t.Fatalf("slept sum = %v, want %v", time.Duration(got), time.Duration(wantSum))
	}
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("len(Delays()) = %d, want 2 (last run only)", got)
	}
}
