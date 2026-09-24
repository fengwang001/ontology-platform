package rfr

import (
	"errors"
	"fmt"
	"testing"
)

// TestRecomputeCount builds a chain v1<-b, v2<-v1, ..., v_{m-1}<-v_{m-2}
// plus a diamond merge v_m<-v1,v_{m-1}; with b changed every view is dirty.
// The unexported counter must equal the dirty-view count m at every scale,
// never growing with the number of reaching paths (shared subtree once).
func TestRecomputeCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New(0)
		vn := func(i int) string { return fmt.Sprintf("v%05d", i) }
		if err := r.AddView(vn(1), []string{"b"}); err != nil {
			t.Fatal(err)
		}
		for i := 2; i < m; i++ {
			if err := r.AddView(vn(i), []string{vn(i - 1)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.AddView(vn(m), []string{vn(1), vn(m - 1)}); err != nil {
			t.Fatal(err)
		}
		if err := r.SetBase("b", 1); err != nil {
			t.Fatal(err)
		}
		log := r.Refresh()
		if r.recomputes != m {
			t.Fatalf("m=%d recomputes=%d want %d", m, r.recomputes, m)
		}
		if len(log) != m {
			t.Fatalf("m=%d log len=%d want %d", m, len(log), m)
		}
		if r.viewVal[vn(m)] != 2 { // v1=1 plus v_{m-1}=1
			t.Fatalf("m=%d merge value=%d want 2", m, r.viewVal[vn(m)])
		}
		for i := 1; i < m; i++ {
			if r.viewVal[vn(i)] != 1 {
				t.Fatalf("m=%d %s=%d want 1", m, vn(i), r.viewVal[vn(i)])
			}
		}
		// A second Refresh with no changes recomputes nothing.
		if got := r.Refresh(); len(got) != 0 || r.recomputes != 0 {
			t.Fatalf("m=%d empty batch recomputed: log=%d counter=%d", m, len(got), r.recomputes)
		}
	}
}

// TestCycleRejection is table-driven over self-edges and multi-node cycles
// that only close when a base is promoted to a view; each rejection must be
// ErrCycle and leave no trace (the graph and base set stay usable).
func TestCycleRejection(t *testing.T) {
	cases := []struct {
		name  string
		build func(*testing.T, *Refresher)
		view  string
		deps  []string
	}{
		{"self-edge", func(t *testing.T, r *Refresher) {
			must(t, r.AddView("a", []string{"c"})) // c becomes a base
		}, "x", []string{"x"}},
		{"promoted-base-closes-cycle", func(t *testing.T, r *Refresher) {
			must(t, r.AddView("a", []string{"c"})) // c becomes a base
			must(t, r.AddView("b", []string{"a"}))
		}, "c", []string{"b"}}, // promote c: c->b->a->c is a cycle
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(0)
			tc.build(t, r)
			if err := r.AddView(tc.view, tc.deps); !errors.Is(err, ErrCycle) {
				t.Fatalf("err=%v want ErrCycle", err)
			}
			if r.g.HasView(tc.view) {
				t.Fatalf("rejected view %q was committed", tc.view)
			}
			if !r.bases["c"] { // c remains a usable base in both cases
				t.Fatalf("base c missing after rejection")
			}
			if err := r.SetBase("c", 9); err != nil {
				t.Fatalf("instance unusable after rejection: %v", err)
			}
		})
	}
}

// TestLastWriteWins verifies that within one batch only the final SetBase
// per base reaches the recomputation.
func TestLastWriteWins(t *testing.T) {
	r := New(0)
	must(t, r.AddView("v", []string{"b"}))
	for _, x := range []int64{1, 2, 3, 42} {
		must(t, r.SetBase("b", x))
	}
	log := r.Refresh()
	if len(log) != 1 || log[0] != (Change{Name: "v", Old: 0, New: 42}) {
		t.Fatalf("log=%v want single v 0->42", log)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
