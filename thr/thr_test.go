package thr

import (
	"fmt"
	"testing"
)

// TestProbeCountConstant pins due-ordered heap lookup: with m far-future
// pending batches, a Tick that fires nothing inspects only the heap root
// (probes == 1), independent of m; after adding one exactly-due batch the
// count is at most 2. probes is read here only, never via an exported API.
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		tt, err := New(1000, int64(m)+1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if err := tt.Record(fmt.Sprintf("k%05d", i), 0, "v"); err != nil {
				t.Fatal(err)
			}
		}
		// All m batches are due at 1000, far above now=0: one root probe.
		if got := tt.Tick(0); len(got) != 0 || tt.probes != 1 {
			t.Fatalf("m=%d phase1: probes=%d fired=%d, want 1 and 0", m, tt.probes, len(got))
		}
		// Add one exactly-due batch (due 1998) after the first Tick, then
		// postpone the m old batches to 1999.
		if err := tt.Record("~due~", 998, "d"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if err := tt.Record(fmt.Sprintf("k%05d", i), 999, "w"); err != nil {
				t.Fatal(err)
			}
		}
		got := tt.Tick(1998)
		if len(got) != 1 || got[0].Key != "~due~" || got[0].Val != "d" {
			t.Fatalf("m=%d phase2: fired=%v, want only ~due~", m, got)
		}
		if tt.probes > 2 {
			t.Fatalf("m=%d phase2: probes=%d grows with m; lookup is not ordered", m, tt.probes)
		}
	}
}
