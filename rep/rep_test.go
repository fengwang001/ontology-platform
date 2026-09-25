package rep

import (
	"strconv"
	"testing"

	"ontology/log"
)

func TestApplyAndGet(t *testing.T) {
	r := New()
	if got := r.Applied(); got != 0 {
		t.Fatalf("initial applied = %d, want 0", got)
	}
	r.Apply(log.Entry{Key: "k", Val: "a", LSN: 1})
	r.Apply(log.Entry{Key: "k", Val: "b", LSN: 2})
	if got := r.Applied(); got != 2 {
		t.Fatalf("applied = %d, want 2", got)
	}
	if v, ok := r.Get("k"); !ok || v != "b" {
		t.Fatalf("Get(k) = %q,%v want b,true", v, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("missing key reported present")
	}
}

func TestCatchUpReplaysClosedInterval(t *testing.T) {
	lg := log.New()
	for _, v := range []string{"a", "b", "c", "d", "e"} {
		lg.Append("k", v)
	}
	r := New()
	r.Apply(log.Entry{Key: "k", Val: "a", LSN: 1})
	r.Apply(log.Entry{Key: "k", Val: "b", LSN: 2}) // replica at applied 2

	got := r.CatchUp(lg, 5) // must replay the closed (2,5] = lsn 3,4,5
	want := []int{3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("replayed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replayed %v, want %v", got, want)
		}
	}
	if r.Applied() != 5 {
		t.Fatalf("applied = %d, want 5", r.Applied())
	}
	if v, _ := r.Get("k"); v != "e" {
		t.Fatalf("k = %q, want e (closed interval includes lsn 5)", v)
	}
}

// TestCatchUpScanBound proves catch-up locates the interval directly by lsn
// instead of scanning the log from the start: one entry behind at any log
// size must examine exactly one entry.
func TestCatchUpScanBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(strconv.Itoa(m), func(t *testing.T) {
			lg := log.New()
			r := New()
			for i := 1; i <= m; i++ {
				e := lg.Append("k", "v")
				if i < m {
					r.Apply(e) // replica ends at applied m-1
				}
			}
			if r.Applied() != m-1 {
				t.Fatalf("m=%d applied = %d, want %d", m, r.Applied(), m-1)
			}
			r.CatchUp(lg, m) // session seen = m: only lsn m is missing
			if r.lastScan != 1 {
				t.Fatalf("m=%d lastScan = %d, want 1 (must not grow with m)", m, r.lastScan)
			}
			if r.Applied() != m {
				t.Fatalf("m=%d applied = %d, want m", m, r.Applied())
			}
			if v, _ := r.Get("k"); v != "v" {
				t.Fatalf("m=%d k = %q, want v", m, v)
			}
		})
	}
}
