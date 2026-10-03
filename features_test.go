package store

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/cond"
)

func TestAfterReserveRollback(t *testing.T) {
	s := NewStore()
	_ = s.SetQuota("t", 100)
	if err := s.SetVersioning("t", true); err != nil {
		t.Fatal(err)
	}

	// Failing storage on the first attempt must not consume a version number,
	// touch usage or set mtime; the retry succeeds as v1.
	var failNext atomic.Bool
	failNext.Store(true)
	s.SetAfterReserve(func() error {
		if failNext.Swap(false) {
			return errInjected
		}
		return nil
	})
	if _, err := s.Put("t", "a", 5, "e1", cond.Cond{}, 10); !IsStorageFailure(err) {
		t.Fatalf("first put = %v, want storage failure", err)
	}
	if s.Used("t") != 0 || s.Current("t", "a").Exists {
		t.Fatalf("rollback left state behind: U=%d exists=%v", s.Used("t"), s.Current("t", "a").Exists)
	}
	r := mustPut(t, s, "t", "a", 5, "e1", cond.Cond{}, 20)
	if r.Version != 1 {
		t.Fatalf("retry consumed number early: v%d want v1", r.Version)
	}

	// Marker append failure must not consume a version number either.
	failNext.Store(true)
	if _, err := s.Delete("t", "a", -1, cond.Cond{}, 30); !IsStorageFailure(err) {
		t.Fatalf("marker delete = %v, want storage failure", err)
	}
	dm, err := s.Delete("t", "a", -1, cond.Cond{}, 40)
	if err != nil || dm.Version != 2 {
		t.Fatalf("marker after rollback = %+v %v, want v2", dm, err)
	}

	// Specified-version permanent delete failure leaves the version in place.
	failNext.Store(true)
	if _, err := s.Delete("t", "a", 1, cond.Cond{}, 50); !IsStorageFailure(err) {
		t.Fatalf("version delete = %v, want storage failure", err)
	}
	if _, ok := s.VersionSize("t", "a", 1); !ok {
		t.Fatal("v1 vanished after failed storage")
	}
	if _, err := s.Delete("t", "a", 1, cond.Cond{}, 60); err != nil {
		t.Fatalf("retry delete v1: %v", err)
	}
}

func TestUnversionedDelete(t *testing.T) {
	s := NewStore()
	_ = s.SetQuota("t", 100)
	mustPut(t, s, "t", "a", 8, "e", cond.Cond{}, 2)
	d, err := s.Delete("t", "a", -1, cond.Cond{}, 3)
	if err != nil || d.Delta != -8 || d.Version != 0 {
		t.Fatalf("delete = %+v %v", d, err)
	}
	if s.Used("t") != 0 || s.Current("t", "a").Exists {
		t.Fatal("object survived unversioned delete")
	}
	if _, err := s.Delete("t", "a", -1, cond.Cond{}, 4); failKind(err) != "notfound" {
		t.Fatalf("second delete = %v", err)
	}
}

// TestDeleteDuringEnableTransition verifies the serial-equivalent ordering
// when versioning is switched on while a Delete is parked in AfterReserve.
func TestDeleteDuringEnableTransition(t *testing.T) {
	s := NewStore()
	_ = s.SetQuota("t", 100)
	mustPut(t, s, "t", "a", 8, "e0", cond.Cond{}, 1)
	proceed := make(chan struct{})
	entered := make(chan struct{}, 1)
	s.SetAfterReserve(func() error {
		entered <- struct{}{}
		<-proceed
		return nil
	})
	type res struct {
		r   DeleteResult
		err error
	}
	done := make(chan res, 1)
	go func() {
		r, err := s.Delete("t", "a", -1, cond.Cond{}, 2)
		done <- res{r, err}
	}()
	<-entered
	if err := s.SetVersioning("t", true); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	out := <-done
	if out.err != nil {
		t.Fatalf("delete during enable: %v", out.err)
	}
	if !out.r.Marker || out.r.Version != 1 {
		t.Fatalf("delete = %+v, want marker v1 (enable then delete)", out.r)
	}
	if s.Used("t") != 8 {
		t.Fatalf("U=%d want 8 (version 0 retained, marker appended)", s.Used("t"))
	}
}

func TestConcurrentIfNoneMatchUniqueWinner(t *testing.T) {
	s := NewStore()
	_ = s.SetQuota("t", 100)
	if err := s.SetVersioning("t", true); err != nil {
		t.Fatal(err)
	}
	const n = 32
	var winners, rejected int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.Put("t", "k", 1, etagAt(i), cond.Cond{IfNoneMatchStar: true}, int64(i))
			if err == nil {
				atomic.AddInt64(&winners, 1)
			} else if failKind(err) == "precond:IfNoneMatch" {
				atomic.AddInt64(&rejected, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if winners != 1 || rejected != n-1 {
		t.Fatalf("winners=%d rejected=%d", winners, rejected)
	}
	if s.Used("t") != 1 {
		t.Fatalf("U=%d want 1", s.Used("t"))
	}
}

func TestConcurrentIfMatchChainOneSuccessor(t *testing.T) {
	s := NewStore()
	_ = s.SetQuota("t", 1_000_000)
	if err := s.SetVersioning("t", true); err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, "t", "k", 1, "e0", cond.Cond{}, 0)
	const n = 32
	var winners int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.Put("t", "k", 1, etagAt(i+1), cond.Cond{IfMatch: strp("e0")}, int64(i+1))
			if err == nil {
				atomic.AddInt64(&winners, 1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if winners != 1 {
		t.Fatalf("successors of e0 = %d, want exactly 1", winners)
	}
}

func TestConcurrentReservationsCannotOvercommit(t *testing.T) {
	s := NewStore()
	_ = s.SetQuota("t", 100)
	const n = 20
	inside := make(chan struct{}, n)
	release := make(chan struct{})
	var rejected int64
	s.SetAfterReserve(func() error {
		inside <- struct{}{}
		<-release
		return nil
	})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Put("t", keyAt(i), 10, etagAt(i), cond.Cond{}, int64(i))
			if err != nil && failKind(err) == "quota" {
				atomic.AddInt64(&rejected, 1)
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-inside
	}
	// 10 puts of 10 bytes hold the whole quota via pending reservations; the
	// other 10 must be rejected even before the hook is allowed to finish.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&rejected) != 10 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt64(&rejected); got != 10 {
		t.Fatalf("rejected while 10 reservations pending = %d, want 10", got)
	}
	close(release)
	wg.Wait()
	if u := s.Used("t"); u != 100 {
		t.Fatalf("U=%d want 100", u)
	}
}

func TestTouchedBoundIndependentOfVersionCount(t *testing.T) {
	for _, n := range []int{10, 10_000} {
		s := NewStore()
		_ = s.SetQuota("t", 10_000_000)
		if err := s.SetVersioning("t", true); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			mustPut(t, s, "t", "k", 1, etagAt(i), cond.Cond{}, int64(i))
		}
		_ = s.Current("t", "k")
		if touched := s.DecisionTouched(); touched > 2 {
			t.Fatalf("n=%d touched=%d, want <= 2", n, touched)
		}
		if touched := s.DecisionTouched(); touched != 1 {
			t.Fatalf("n=%d head read touched=%d, want 1", n, touched)
		}
	}
}

func etagAt(i int) string { return "e" + itoa(i) }
func keyAt(i int) string  { return "k" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
