package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// Invariant 1: Get equals naive last-write-wins, key by key.
func TestGetMatchesNaive(t *testing.T) {
	st, err := api.New(7, 3, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	naive := map[string]string{}
	// Deterministic pseudo-random sequence over a small key space.
	for i := 0; i < 2000; i++ {
		k := fmt.Sprintf("k%d", (i*37)%50)
		v := fmt.Sprintf("v%d", i)
		if err := st.Append(k, v); err != nil {
			t.Fatal(err)
		}
		naive[k] = v
	}
	for k, want := range naive {
		if got, ok := st.Get(k); !ok || got != want {
			t.Fatalf("Get(%q)=%q,%v want %q", k, got, ok, want)
		}
	}
	if _, ok := st.Get("never-written"); ok {
		t.Fatal("Get on absent key returned ok")
	}
}

// Invariant 2: exact logical/physical/amplification per step (NOTES.md trace).
func TestAmplificationAccounting(t *testing.T) {
	st, err := api.New(6, 2, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		k, v     string
		logical  int64
		physical int64
	}{
		{"a", "1", 2, 0},
		{"b", "1", 4, 0},
		{"c", "1", 6, 6},   // flush 6
		{"d", "12", 9, 6},  //
		{"a", "12", 12, 22}, // flush 6 + merge 10
		{"e", "1", 14, 22},
	}
	for i, s := range steps {
		if err := st.Append(s.k, s.v); err != nil {
			t.Fatal(err)
		}
		if st.LogicalBytes() != s.logical || st.PhysicalBytes() != s.physical {
			t.Fatalf("step %d: got %d/%d, want %d/%d",
				i+1, st.LogicalBytes(), st.PhysicalBytes(), s.logical, s.physical)
		}
		if got := st.Amplification(); got != float64(s.physical)/float64(s.logical) {
			t.Fatalf("step %d: amp %v", i+1, got)
		}
	}
	// Merge bytes are counted: physical is 22, not the flush-only 12.
	if st.PhysicalBytes() == 12 {
		t.Fatal("merge physical bytes not counted")
	}
	if got, want := st.Amplification(), 22.0/14.0; got != want {
		t.Fatalf("final amp %v, want %v", got, want)
	}
	if v, ok := st.Get("a"); !ok || v != "12" {
		t.Fatalf("Get(a)=%q,%v want 12,true", v, ok)
	}
}

// Invariant 4: every failure is a distinct sentinel and leaves no trace.
func TestFailuresLeaveNoTrace(t *testing.T) {
	for _, c := range []struct{ b, tm, m int }{{0, 2, 4}, {-1, 2, 4}, {6, 1, 4}, {6, 0, 4}, {6, 2, 0}, {6, 2, -3}} {
		if _, err := api.New(c.b, c.tm, c.m); !errors.Is(err, api.ErrBadParams) {
			t.Fatalf("New(%d,%d,%d) err=%v", c.b, c.tm, c.m, err)
		}
	}
	st, err := api.New(6, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Append("ab", "12"); err != nil {
		t.Fatal(err)
	}
	lo, ph, amp := st.LogicalBytes(), st.PhysicalBytes(), st.Amplification()
	if err := st.Append("", "x"); !errors.Is(err, api.ErrEmptyKey) {
		t.Fatalf("empty key err=%v", err)
	}
	if err := st.Append("abc", "de"); !errors.Is(err, api.ErrRecordTooLarge) {
		t.Fatalf("oversize err=%v", err)
	}
	if api.ErrEmptyKey == api.ErrRecordTooLarge || api.ErrRecordTooLarge == api.ErrBadParams ||
		api.ErrEmptyKey == api.ErrBadParams {
		t.Fatal("sentinel errors not distinct")
	}
	if st.LogicalBytes() != lo || st.PhysicalBytes() != ph || st.Amplification() != amp {
		t.Fatal("rejected append changed state")
	}
	if err := st.Append("cd", "34"); err != nil { // still usable
		t.Fatal(err)
	}
	if v, ok := st.Get("cd"); !ok || v != "34" {
		t.Fatal("store unusable after rejections")
	}
}

// Section 6: concurrent appends of disjoint keys + concurrent readers.
func TestConcurrentAppendGet(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		st, err := api.New(16, 4, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for g := 0; g < n; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				<-start
				for j := 0; j < 20; j++ {
					if err := st.Append(fmt.Sprintf("g%04d-k%d", g, j), fmt.Sprintf("v%d", j)); err != nil {
						t.Error(err)
					}
				}
			}(g)
		}
		close(start)
		wg.Wait()
		var wantLogical int64
		for g := 0; g < n; g++ {
			for j := 0; j < 20; j++ {
				k := fmt.Sprintf("g%04d-k%d", g, j)
				wantLogical += int64(len(k) + len(fmt.Sprintf("v%d", j)))
				if v, ok := st.Get(k); !ok || v != fmt.Sprintf("v%d", j) {
					t.Fatalf("n=%d Get(%q)=%q,%v", n, k, v, ok)
				}
			}
		}
		if st.LogicalBytes() != wantLogical {
			t.Fatalf("n=%d logical=%d want %d", n, st.LogicalBytes(), wantLogical)
		}
		// Concurrent readers on the filled instance see consistent results.
		var rwg sync.WaitGroup
		for r := 0; r < n; r++ {
			rwg.Add(1)
			go func() {
				defer rwg.Done()
				for j := 0; j < 20; j++ {
					st.Get("g0000-k0")
					st.LogicalBytes()
					st.PhysicalBytes()
					st.Amplification()
				}
				if err := st.SelfCheck(); err != nil {
					t.Error(err)
				}
			}()
		}
		rwg.Wait()
	}
}
