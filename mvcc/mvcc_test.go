package mvcc_test

// External black-box tests for mvcc and api; the chain white-box test that
// reads the unexported counter stays in package chain.
import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/mvcc"
)

func TestSnapshotIsolation(t *testing.T) { // invariant 1
	s := mvcc.New()
	s.Write("k", "v1")
	snap := s.Snapshot()
	s.Write("k", "v2")
	s.Write("late", "x")
	if v, ok := s.Read("k", snap); !ok || v != "v1" {
		t.Fatalf("Read(k,%d)=(%q,%v), want v1", snap, v, ok)
	} else if v, ok := s.Read("late", snap); ok || v != "" {
		t.Fatalf("post-snapshot key visible: (%q,%v)", v, ok)
	}
}
func TestNaiveReference(t *testing.T) { // invariant 2: keep all history, max ver <= s
	cases := []struct {
		writes []string
		snap   int64
		want   string
		ok     bool
	}{
		{nil, 0, "", false},
		{[]string{"a"}, 1, "a", true},
		{[]string{"a"}, 0, "", false},
		{[]string{"a", "b", "c"}, 2, "b", true},
		{[]string{"a", "b", "c"}, 3, "c", true},
	}
	for _, tc := range cases {
		s := mvcc.New()
		hist := map[string]int64{}
		for _, w := range tc.writes {
			hist[w] = s.Write("k", w)
		}
		var best int64 = -1
		bestVal := ""
		for val, v := range hist {
			if v <= tc.snap && v > best {
				best, bestVal = v, val
			}
		}
		got, ok := s.Read("k", tc.snap)
		if ok != tc.ok || (ok && got != tc.want) || (ok && got != bestVal) {
			t.Fatalf("writes=%v snap=%d: (%q,%v), want (%q,%v)=naive %q",
				tc.writes, tc.snap, got, ok, tc.want, tc.ok, bestVal)
		}
	}
}
func TestCollectSafety(t *testing.T) { // invariant 3: active reads survive Collect; head never dropped
	s := mvcc.New()
	s.Write("k", "v1")
	A := s.Snapshot()
	s.Write("k", "v2")
	B := s.Snapshot()
	s.Write("k", "v3")
	before, _ := s.Read("k", B)
	s.Release(A)
	if n := s.Collect(); n != 1 { // v1's [1,2) holds no active snapshot
		t.Fatalf("Collect removed %d, want 1", n)
	}
	if v, ok := s.Read("k", B); !ok || v != before || before != "v2" {
		t.Fatalf("active B changed across Collect: (%q,%v), want %q", v, ok, before)
	} else if _, ok := s.Read("k", 3); !ok {
		t.Fatal("head version was collected")
	}
}
func TestConcurrentOldSnapshot(t *testing.T) { // section 6, no sleeps
	s := mvcc.New()
	s.Write("k", "seed")
	old := s.Snapshot()
	const readers, loops, writes = 16, 100, 3000
	var wg sync.WaitGroup
	wg.Add(readers + 1)
	var mu sync.Mutex
	bad := false
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				if v, ok := s.Read("k", old); !ok || v != "seed" {
					mu.Lock()
					bad = true
					mu.Unlock()
					return
				}
			}
		}()
	}
	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			s.Write("k", fmt.Sprintf("w%d", i))
		}
	}()
	wg.Wait()
	if bad {
		t.Fatal("an old-snapshot reader observed a newer value")
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) { // invariant 4
	m := api.New()
	m.Write("k", "v0")
	sn := m.Snapshot()
	m.Write("k", "v1")
	cur := int64(2)
	cases := []struct {
		call func() error
		want error
	}{
		{func() error { _, e := m.Write("", "x"); return e }, api.ErrEmptyKey},
		{func() error { _, _, e := m.Read("", 1); return e }, api.ErrEmptyKey},
		{func() error { _, _, e := m.Read("k", -1); return e }, api.ErrInvalidSnapshot},
		{func() error { _, _, e := m.Read("k", cur+1); return e }, api.ErrInvalidSnapshot},
		{func() error { return m.Release(cur + 1) }, api.ErrInvalidSnapshot},
		{func() error { return m.Release(0) }, api.ErrSnapshotInactive},
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("err=%v want %v", err, tc.want)
		}
	}
	if m.Release(sn) != nil {
		t.Fatal("releasing an active snapshot failed")
	}
	if err := m.Release(sn); !errors.Is(err, api.ErrSnapshotInactive) {
		t.Fatalf("second release err=%v", err)
	}
	if v, ok, _ := m.Read("k", 1); !ok || v != "v0" {
		t.Fatalf("rejected ops mutated history: (%q,%v)", v, ok)
	}
	if tv, _ := m.Write("z", "live"); tv != cur+1 {
		t.Fatalf("version after rejections=%d want %d", tv, cur+1)
	}
}
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
