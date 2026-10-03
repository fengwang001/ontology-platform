package sampler

import (
	"errors"
	"testing"

	"sampler/report"
)

func mustNew(t *testing.T, n, m, w, kt, tmax uint64) *Sampler {
	t.Helper()
	s, err := New(n, m, w, kt, tmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func rec(t *testing.T, s *Sampler, now uint64, tn, key string, sev int) (bool, []report.Summary) {
	t.Helper()
	kept, sums, err := s.Record(now, tn, key, sev)
	if err != nil {
		t.Fatalf("Record(now=%d,%s,%s,sev=%d): %v", now, tn, key, sev, err)
	}
	t.Logf("Record(now=%d,%s,%s,sev=%d) -> kept=%v sums=%v", now, tn, key, sev, kept, sums)
	return kept, sums
}

// TestKeepPattern: N=2,M=3 example — kept at cnt 1,2,5,8; dropped=4;
// (cnt-N) hits exact multiples of M (3,6) and off-by-one neighbours (4,7 drop).
func TestKeepPattern(t *testing.T) {
	s := mustNew(t, 2, 3, 1000, 10, 10)
	want := []bool{true, true, false, false, true, false, false, true}
	for i, w := range want {
		if kept, _ := rec(t, s, 100, "t", "k", 1); kept != w {
			t.Fatalf("record %d: kept=%v want %v", i+1, kept, w)
		}
	}
	if d, ok := s.Pending("t", "k"); !ok || d != 4 {
		t.Fatalf("Pending = %d,%v want 4,true", d, ok)
	}
	kept, sums := rec(t, s, 1500, "t", "k", 1) // window roll
	if !kept || len(sums) != 1 || sums[0] != (report.Summary{Tenant: "t", Key: "k", Window: 0, Dropped: 4, Reason: report.Rolled}) {
		t.Fatalf("roll: kept=%v sums=%v", kept, sums)
	}
	if d, _ := s.Pending("t", "k"); d != 0 {
		t.Fatalf("Pending after Rolled = %d want 0", d)
	}
}

// TestEdgeParams: N=0 (pure 1/M sampling), M=1 (keep all after N),
// window boundary exactly at W, sev=4 keeps without consuming quota.
func TestEdgeParams(t *testing.T) {
	s := mustNew(t, 0, 3, 1000, 10, 10) // N=0: keep when cnt%3==0
	want := []bool{false, false, true, false, false, true}
	for i, w := range want {
		if kept, _ := rec(t, s, 0, "t", "k", 1); kept != w {
			t.Fatalf("N=0 record %d: kept=%v want %v", i+1, kept, w)
		}
	}
	s2 := mustNew(t, 1, 1, 1000, 10, 10) // M=1: everything kept
	for i := 0; i < 5; i++ {
		if kept, _ := rec(t, s2, 0, "t", "k", 1); !kept {
			t.Fatalf("M=1 record %d dropped", i+1)
		}
	}
	s3 := mustNew(t, 1, 5, 1000, 10, 10) // boundary: now==W is next window
	rec(t, s3, 999, "t", "k", 1)
	rec(t, s3, 999, "t", "k", 1)
	rec(t, s3, 999, "t", "k", 1) // win0: cnt=2,3 -> drop, dropped=2
	kept, sums := rec(t, s3, 1000, "t", "k", 1)
	if !kept || len(sums) != 1 || sums[0].Window != 0 || sums[0].Dropped != 2 {
		t.Fatalf("boundary now==W: kept=%v sums=%v", kept, sums)
	}
	s4 := mustNew(t, 2, 3, 1000, 10, 10) // sev=4 keeps, no quota consumed
	rec(t, s4, 0, "t", "k", 1)
	rec(t, s4, 0, "t", "k", 1) // cnt=2
	if kept, _ := rec(t, s4, 0, "t", "k", 4); !kept {
		t.Fatal("sev=4 must be kept")
	}
	if kept, _ := rec(t, s4, 0, "t", "k", 1); kept {
		t.Fatal("sev=4 must not consume quota: cnt=3 -> drop")
	}
	t.Logf("edge: N=0 keeps cnt%%3==0; M=1 keeps all; now==W rolls; sev=4 free")
}

// TestEviction: LRU by access order (a dropped record still refreshes),
// Evicted summary delivered, evicted key restarts quota, no Rolled on miss.
func TestEviction(t *testing.T) {
	s := mustNew(t, 1, 1, 1000, 2, 10)
	rec(t, s, 0, "t", "a", 1)
	rec(t, s, 0, "t", "a", 1)
	_, sums := rec(t, s, 0, "t", "b", 1)
	if len(sums) != 0 {
		t.Fatalf("no eviction yet: %v", sums)
	}
	rec(t, s, 0, "t", "a", 1) // touch a -> b is LRU
	_, sums = rec(t, s, 0, "t", "c", 1)
	if len(sums) != 0 { // b had no drops -> no Evicted summary
		t.Fatalf("b had dropped=0, want no summary: %v", sums)
	}
	if _, ok := s.Pending("t", "b"); ok {
		t.Fatal("b must be evicted (LRU), a was touched most recently")
	}
	if _, ok := s.Pending("t", "a"); !ok {
		t.Fatal("a must survive: eviction follows access order")
	}
	if kept, sums := rec(t, s, 0, "t", "b", 1); !kept || len(sums) != 0 {
		t.Fatalf("b restarts fresh: kept=%v sums=%v (no Rolled on new entry)", kept, sums)
	}
	t.Logf("Kt=2: touch a keeps it; c evicts b (LRU); b replay starts cnt=1, kept")
}

// TestEvictedDropped: Evicted summary carries dropped; Rolled/Evicted exclusive.
func TestEvictedDropped(t *testing.T) {
	s := mustNew(t, 1, 2, 1000, 2, 10)
	rec(t, s, 0, "t", "a", 1)
	rec(t, s, 0, "t", "a", 1)
	rec(t, s, 0, "t", "a", 1) // a dropped=1
	rec(t, s, 0, "t", "b", 1)
	_, sums := rec(t, s, 0, "t", "c", 1) // evicts a
	if len(sums) != 1 || sums[0] != (report.Summary{Tenant: "t", Key: "a", Window: 0, Dropped: 1, Reason: report.Evicted}) {
		t.Fatalf("Evicted summary: %v", sums)
	}
	if d, ok := s.Pending("t", "a"); ok || d != 0 {
		t.Fatalf("evicted a must be gone: %d,%v", d, ok)
	}
}

// TestRejectedNoState: param/clock/tenant-limit rejections change nothing.
func TestRejectedNoState(t *testing.T) {
	s := mustNew(t, 1, 2, 1000, 2, 1)
	rec(t, s, 100, "t1", "a", 1)
	rec(t, s, 100, "t1", "a", 1)
	rec(t, s, 100, "t1", "a", 1) // dropped=1
	for _, bad := range []struct {
		tn, key string
		sev     int
	}{{"", "k", 1}, {"t", "", 1}, {"t", "k", 6}, {"t", "k", -1}} {
		if _, _, err := s.Record(100, bad.tn, bad.key, bad.sev); !errors.Is(err, ErrParam) {
			t.Fatalf("bad param %+v: %v", bad, err)
		}
	}
	if _, _, err := s.Record(99, "t1", "a", 1); !errors.Is(err, ErrClock) {
		t.Fatalf("clock: %v", err)
	}
	if _, _, err := s.Record(100, "t2", "z", 1); !errors.Is(err, ErrTenantLimit) {
		t.Fatalf("tenant limit: %v", err)
	}
	if d, _ := s.Pending("t1", "a"); d != 1 {
		t.Fatalf("rejected ops changed state: pending=%d want 1", d)
	}
	if kept, _ := rec(t, s, 100, "t1", "a", 1); kept {
		t.Fatal("quota must be unaffected by rejected records: cnt=4 -> drop")
	}
	t.Logf("rejections (param,clock,tenant) left pending=1 and cnt intact")
}

// TestFlush: Closed summaries in (tenant,key) order; sink failure at the
// j-th item keeps it and the rest; retry delivers them.
func TestFlush(t *testing.T) {
	s := mustNew(t, 0, 2, 1000, 10, 10) // N=0,M=2: even cnt kept
	for _, k := range []string{"b", "a"} {
		rec(t, s, 0, "t1", k, 1) // cnt=1 drop
	}
	rec(t, s, 0, "t2", "a", 1) // cnt=1 drop
	if _, err := s.Flush(2000, nil); !errors.Is(err, ErrParam) {
		t.Fatalf("nil sink: %v", err)
	}
	var got []report.Summary
	failAt := 2 // fail on the 2nd delivery (t1/b)
	n, err := s.Flush(2000, func(sm report.Summary) error {
		got = append(got, sm)
		if len(got) == failAt {
			return errors.New("boom")
		}
		return nil
	})
	if n != 1 || err == nil || len(got) != 2 {
		t.Fatalf("flush fail: n=%d err=%v got=%v", n, err, got)
	}
	if got[0] != (report.Summary{Tenant: "t1", Key: "a", Window: 0, Dropped: 1, Reason: report.Closed}) {
		t.Fatalf("first summary: %v", got[0])
	}
	if d, _ := s.Pending("t1", "a"); d != 0 {
		t.Fatalf("delivered t1/a must be cleared: %d", d)
	}
	if d, _ := s.Pending("t1", "b"); d != 1 {
		t.Fatalf("failed t1/b must be kept: %d", d)
	}
	got = got[:0]
	n, err = s.Flush(2000, func(sm report.Summary) error { got = append(got, sm); return nil })
	if n != 2 || err != nil || got[0].Key != "b" || got[1].Tenant != "t2" {
		t.Fatalf("retry: n=%d err=%v got=%v", n, err, got)
	}
	t.Logf("flush order (t1,a),(t1,b),(t2,a); fail@2 kept b,t2/a; retry delivered 2")
}
