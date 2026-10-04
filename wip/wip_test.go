package wip_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/routing"
	"ontology/wip"
)

func newEnv(t *testing.T, n int, back []int, insp []bool, r int) (*routing.Registry, *wip.Manager, string) {
	t.Helper()
	reg := routing.NewRegistry()
	rid := "route-" + t.Name()
	if _, err := reg.Define(rid, n, back, insp, r); err != nil {
		t.Fatal(err)
	}
	return reg, wip.NewManager(reg), rid
}

func assertConservation(t *testing.T, m *wip.Manager, wo string) {
	t.Helper()
	st, err := m.Snapshot(wo)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for c, v := range st.Queue {
		if v <= 0 {
			t.Fatalf("non-positive cell %+v=%d", c, v)
		}
		if c.K() > st.R {
			t.Fatalf("cell above R: %+v", c)
		}
		sum += v
	}
	if sum != st.WIP {
		t.Fatalf("%s: queue sum %d != wip %d", wo, sum, st.WIP)
	}
	if st.Q != st.Done+st.Scrapped+sum {
		t.Fatalf("%s: Q=%d done=%d scrap=%d sum=%d", wo, st.Q, st.Done, st.Scrapped, sum)
	}
}

func TestReportFlow(t *testing.T) {
	_, m, rid := newEnv(t, 3, []int{1, 2, 2}, []bool{false, false, false}, 1)
	if err := m.Open("wo", rid, 100); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 1, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	assertConservation(t, m, "wo")
	st, _ := m.Snapshot("wo")
	if st.At(2, 0) != 100 {
		t.Fatalf("got %d", st.At(2, 0))
	}
	// exact cell quantity: totals equal to the cell amount
	if err := m.Report("wo", 2, 0, 90, 4, 6); err != nil {
		t.Fatal(err)
	}
	st, _ = m.Snapshot("wo")
	if st.At(3, 0) != 90 || st.Scrapped != 4 || st.At(2, 1) != 6 {
		t.Fatalf("state=%+v queue=%v", st, st.Queue)
	}
	if st.Touched != 3 {
		t.Fatalf("touched=%d want 3", st.Touched)
	}
	// k == R: rework refused, good/scrap accepted
	if err := m.Report("wo", 2, 1, 5, 0, 1); !errors.Is(err, wip.ErrReworkCap) {
		t.Fatalf("want rework cap, got %v", err)
	}
	if err := m.Report("wo", 2, 1, 5, 1, 0); err != nil {
		t.Fatal(err)
	}
	st, _ = m.Snapshot("wo")
	if st.At(3, 1) != 5 || st.Scrapped != 5 {
		t.Fatalf("queue=%v scrap=%d", st.Queue, st.Scrapped)
	}
	assertConservation(t, m, "wo")
	// last operation good -> done
	if err := m.Report("wo", 3, 0, 90, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 3, 1, 5, 0, 0); err != nil {
		t.Fatal(err)
	}
	res, err := m.Close("wo")
	if err != nil {
		t.Fatal(err)
	}
	if res.Done != 95 || res.Scrapped != 5 || res.Shortage != 5 {
		t.Fatalf("%+v", res)
	}
}

func TestReworkToEarlierAndSelf(t *testing.T) {
	// back=[1,1,2]: op2 and op3 both loop back to op1/op2
	_, m, rid := newEnv(t, 3, []int{1, 1, 2}, []bool{false, false, false}, 3)
	if err := m.Open("wo", "missing-route", 10); !errors.Is(err, wip.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := m.Open("wo", rid, 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 1, 0, 0, 0, 10); err != nil { // self loop
		t.Fatal(err)
	}
	st, _ := m.Snapshot("wo")
	if st.At(1, 1) != 10 {
		t.Fatalf("self loop queue=%v", st.Queue)
	}
	if err := m.Report("wo", 1, 1, 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 2, 1, 0, 0, 10); err != nil { // back to op1
		t.Fatal(err)
	}
	// the 10 units are now at (1,2); move them through op2 then rework at op3
	if err := m.Report("wo", 1, 2, 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 2, 2, 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("wo", 3, 2, 0, 0, 10); err != nil { // back to op2
		t.Fatal(err)
	}
	st, _ = m.Snapshot("wo")
	if st.At(2, 3) != 10 || len(st.Queue) != 1 {
		t.Fatalf("queue=%v", st.Queue)
	}
	assertConservation(t, m, "wo")
}

func TestRejectionOrdering(t *testing.T) {
	_, m, rid := newEnv(t, 2, []int{1, 2}, []bool{false, false}, 0)

	// invalid arguments beat everything
	if err := m.Report("", 1, 0, 1, 0, 0); !errors.Is(err, wip.ErrInvalid) {
		t.Fatal(err)
	}
	if err := m.Report("ghost", 1, 0, 1, 0, 0); !errors.Is(err, wip.ErrNotFound) {
		t.Fatal(err)
	}
	if err := m.Open("w", rid, 10); err != nil {
		t.Fatal(err)
	}
	if err := m.Open("w", rid, 10); !errors.Is(err, wip.ErrConflict) {
		t.Fatal(err)
	}
	// out of range index beats quantity checks
	if err := m.Report("w", 9, 0, 1, 0, 0); !errors.Is(err, wip.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	if err := m.Report("w", 1, 1, 1, 0, 0); !errors.Is(err, wip.ErrInvalid) { // k>R
		t.Fatalf("got %v", err)
	}
	// zero sum invalid
	if err := m.Report("w", 1, 0, 0, 0, 0); !errors.Is(err, wip.ErrInvalid) {
		t.Fatal(err)
	}
	// over quantity beats rework-cap: 11 > 10 although k==R with rework>0
	if err := m.Report("w", 1, 0, 0, 0, 11); !errors.Is(err, wip.ErrOverQty) {
		t.Fatalf("got %v", err)
	}
	// within quantity + rework at cap
	if err := m.Report("w", 1, 0, 0, 0, 10); !errors.Is(err, wip.ErrReworkCap) {
		t.Fatalf("got %v", err)
	}
	// split validation order
	if err := m.Split("w", "", 1, 0, 1); !errors.Is(err, wip.ErrInvalid) {
		t.Fatal(err)
	}
	if err := m.Split("ghost", "n", 1, 0, 1); !errors.Is(err, wip.ErrNotFound) {
		t.Fatal(err)
	}
	if err := m.Split("w", "n", 1, 0, 11); !errors.Is(err, wip.ErrOverQty) {
		t.Fatal(err)
	}
	// close while wip non-zero is state error
	if _, err := m.Close("w"); !errors.Is(err, wip.ErrState) {
		t.Fatalf("got %v", err)
	}
}

func TestSplitAndClose(t *testing.T) {
	_, m, rid := newEnv(t, 3, []int{1, 2, 2}, []bool{false, false, false}, 1)
	if err := m.Open("a", rid, 100); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("a", 1, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("a", 2, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Split("a", "b", 3, 0, 30); err != nil {
		t.Fatal(err)
	}
	stA, _ := m.Snapshot("a")
	stB, err := m.Snapshot("b")
	if err != nil {
		t.Fatal(err)
	}
	if stA.Q != 70 || stA.At(3, 0) != 70 {
		t.Fatalf("a Q=%d cell=%d", stA.Q, stA.At(3, 0))
	}
	if stB.Q != 30 || stB.At(3, 0) != 30 || stB.Done != 0 || stB.Scrapped != 0 || stB.Held || stB.Closed {
		t.Fatalf("b=%+v", stB)
	}
	assertConservation(t, m, "a")
	assertConservation(t, m, "b")
	// combined piece count unchanged
	if stA.Q+stB.Q != 100 {
		t.Fatalf("combined Q=%d", stA.Q+stB.Q)
	}
	if err := m.Split("a", "b", 3, 0, 1); !errors.Is(err, wip.ErrState) { // newWo exists
		t.Fatalf("got %v", err)
	}
	// finish both
	if err := m.Report("a", 3, 0, 70, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("b", 3, 0, 30, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Close("b"); err != nil {
		t.Fatal(err)
	}
	if err := m.Report("a", 1, 0, 1, 0, 0); !errors.Is(err, wip.ErrClosed) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Close("a"); !errors.Is(err, wip.ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

func TestTouchedBudget(t *testing.T) {
	sizes := []struct {
		n, r int
		back []int
	}{
		{4, 2, []int{1, 2, 2, 2}},
		{32, 1000, func() []int {
			b := make([]int, 32)
			for i := range b {
				b[i] = i + 1
			}
			return b
		}()},
	}
	for _, sz := range sizes {
		name := strings.ReplaceAll(t.Name()+"-size", "/", "_")
		reg := routing.NewRegistry()
		rid := name
		if _, err := reg.Define(rid, sz.n, sz.back, make([]bool, sz.n), sz.r); err != nil {
			t.Fatal(err)
		}
		m := wip.NewManager(reg)
		wo := "wo"
		if err := m.Open(wo, rid, 1_000_000_000); err != nil {
			t.Fatal(err)
		}
		for step := 0; step < 200; step++ {
			st, _ := m.Snapshot(wo)
			if st.Touched > 3 {
				t.Fatalf("n=%d r=%d touched=%d", sz.n, sz.r, st.Touched)
			}
			for c := range st.Queue {
				if c.K() > sz.r {
					t.Fatal("level above R")
				}
			}
			assertConservation(t, m, wo)
			// arbitrary valid report
			i := 1 + step%sz.n
			k := step % (sz.r + 1)
			if st.At(i, k) == 0 {
				continue
			}
			q := st.At(i, k)
			g, s, rw := q, int64(0), int64(0)
			if step%4 == 0 && k < sz.r {
				g, s, rw = q/2, 0, q-q/2
			}
			if err := m.Report(wo, i, k, g, s, rw); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
		}
	}
}

func TestConcurrentReports(t *testing.T) {
	_, m, rid := newEnv(t, 2, []int{1, 2}, []bool{false, false}, 0)
	if err := m.Open("c", rid, 1000); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				// every attempt is all-scrap of 1 or fails; total scrap is bounded
				if err := m.Report("c", 1, 0, 0, 1, 0); err != nil &&
					!errors.Is(err, wip.ErrOverQty) {
					t.Errorf("err %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	st, _ := m.Snapshot("c")
	if st.Q != st.Scrapped+st.WIP {
		t.Fatalf("Q=%d scrap=%d wip=%d", st.Q, st.Scrapped, st.WIP)
	}
	if st.Scrapped > 1000 {
		t.Fatalf("scrap=%d", st.Scrapped)
	}
}
