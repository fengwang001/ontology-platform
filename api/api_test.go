package api

import (
	"errors"
	"math/rand"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func noerr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func nfreed(t *testing.T, r []int, n int) {
	t.Helper()
	if len(r) != n {
		t.Fatalf("reclaimed %d want %d (%v)", len(r), n, r)
	}
}
func TestNaiveEquivalence(t *testing.T) {
	noerr(t, New().SelfCheck())
	e, rng := New(), rand.New(rand.NewSource(7))
	ops := []func(){
		func() { _ = e.Enter(rng.Intn(4)) },
		func() { _ = e.Exit(rng.Intn(4)) },
		func() { _ = e.Retire(1 + rng.Intn(10)) },
		func() { _ = e.Retire(-rng.Intn(3)) },
		func() { e.AdvanceEpoch() },
		func() {
			want := expectReclaim(e.Snapshot())
			if got := e.Reclaim(); !reflect.DeepEqual(got, want) {
				t.Fatalf("reclaim %v want %v", got, want)
			}
		},
	}
	for i := 0; i < 4000; i++ {
		ops[rng.Intn(len(ops))]()
	}
}
func TestEightSteps(t *testing.T) {
	e := New()
	for _, err := range []error{e.Enter(1), e.Enter(2), e.Retire(10), e.Exit(1)} {
		noerr(t, err)
	}
	e.AdvanceEpoch()
	nfreed(t, e.Reclaim(), 0)
	if s := e.Snapshot(); s.G != 1 || !reflect.DeepEqual(s.Active, map[int]int64{2: 0}) || len(s.Retired) != 1 {
		t.Fatalf("step 6 snapshot %+v", s)
	}
	noerr(t, e.Exit(2))
	if r := e.Reclaim(); !reflect.DeepEqual(r, []int{10}) {
		t.Fatalf("step 8 %v want [10]", r)
	}
}

func TestEventualReclaim(t *testing.T) {
	for _, n := range []int{1, 10, 100} {
		e := New()
		for i := 0; i < n; i++ {
			noerr(t, e.Enter(i))
			noerr(t, e.Retire(100+i))
		}
		nfreed(t, e.Reclaim(), 0)
		e.AdvanceEpoch()
		nfreed(t, e.Reclaim(), 0)
		for i := 0; i < n; i++ {
			_ = e.Exit(i)
		}
		nfreed(t, e.Reclaim(), n)
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	cases := []struct {
		do   func(*EBR) error
		want error
	}{
		{func(e *EBR) error { return e.Enter(1) }, ErrDuplicateEnter},
		{func(e *EBR) error { return e.Exit(2) }, ErrNotActive},
		{func(e *EBR) error { return e.Retire(0) }, ErrInvalidNode},
		{func(e *EBR) error { return e.Retire(10) }, ErrAlreadyRetired},
	}
	for _, c := range cases {
		e := New()
		_ = e.Enter(1)
		_ = e.Retire(10)
		before := e.Snapshot()
		if err := c.do(e); !errors.Is(err, c.want) || !reflect.DeepEqual(before, e.Snapshot()) {
			t.Fatalf("wrong error or state trace: %v", err)
		}
		noerr(t, e.Retire(11))
	}
}

func TestSentinelsDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range []error{ErrDuplicateEnter, ErrNotActive, ErrInvalidNode, ErrAlreadyRetired} {
		if seen[s.Error()] {
			t.Fatal("sentinels not distinct")
		}
		seen[s.Error()] = true
	}
}

func TestConcurrent(t *testing.T) {
	const N = 64
	e := New()
	var ann [N]atomic.Int64     // announced epoch + 1; 0 means inactive
	var rep [N + 1]atomic.Int64 // node retirement epoch
	var once [N + 1]atomic.Bool
	var freed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = e.Enter(i)
			ann[i].Store(e.Snapshot().Active[i] + 1)
			_ = e.Retire(i + 1)
			for _, it := range e.Snapshot().Retired {
				if it.Node == i+1 {
					rep[i+1].Store(it.Epoch)
				}
			}
			_ = e.Exit(i)
			ann[i].Store(0)
		}(i)
	}
	for freed.Load() < N {
		e.AdvanceEpoch()
		for _, id := range e.Reclaim() {
			for j := range ann {
				if a := ann[j].Load(); a > 0 && a-1 <= rep[id].Load() {
					t.Errorf("unsafe free node %d while worker %d active", id, j)
				}
			}
			if once[id].Swap(true) {
				t.Errorf("node %d reclaimed twice", id)
			}
			freed.Add(1)
		}
		runtime.Gosched()
	}
	wg.Wait()
}
