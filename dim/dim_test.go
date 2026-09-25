package dim

import (
	"errors"
	"fmt"
	"testing"
)

func ent(k, v string) Entry { return Entry{Key: k, Val: v} }

func TestByteAccounting(t *testing.T) {
	cases := []struct {
		name  string
		batch []Entry
		want  int64 // snapshot bytes after this broadcast
	}{
		{"two one-char keys", []Entry{ent("a", "1"), ent("b", "2")}, 18},
		{"two more", []Entry{ent("c", "3"), ent("d", "4")}, 36},
		{"upsert no double count", []Entry{ent("a", "9")}, 36}, // a already present
		{"new long key", []Entry{ent("e", "5"), ent("f", "6")}, 54},
	}
	s, _ := NewStore(1 << 30)
	for _, c := range cases {
		if _, err := s.Broadcast(c.batch); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := s.hist[s.V()].bytes; got != c.want {
			t.Errorf("%s: snapshot bytes=%d want %d", c.name, got, c.want)
		}
	}
	if s.Used() != 18+36+36+54 { // all four versions retained
		t.Errorf("used=%d want %d", s.Used(), 18+36+36+54)
	}
}
func TestEvictionOrderAndFloor(t *testing.T) {
	s, _ := NewStore(100)
	steps := [][]Entry{
		{ent("a", "1"), ent("b", "2")},
		{ent("c", "3"), ent("d", "4")},
		{ent("a", "9")},
		{ent("e", "5"), ent("f", "6")}, // forces eviction
	}
	wantVersions := [][]int64{{0, 1}, {0, 1, 2}, {0, 1, 2, 3}, {3, 4}}
	wantUsed := []int64{18, 54, 90, 90}
	for i, b := range steps {
		if _, err := s.Broadcast(b); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if got := s.Versions(); fmt.Sprint(got) != fmt.Sprint(wantVersions[i]) {
			t.Errorf("step %d versions=%v want %v", i+1, got, wantVersions[i])
		}
		if s.Used() != wantUsed[i] {
			t.Errorf("step %d used=%d want %d", i+1, s.Used(), wantUsed[i])
		}
	}
	// Floor: the current V and V-1 are always the last two retained.
	if vs := s.Versions(); len(vs) < 2 || vs[len(vs)-1] != 4 || vs[len(vs)-2] != 3 {
		t.Errorf("floor violated: %v", vs)
	}
	if _, k := s.Lookup(1, "a"); k != KindStale { // v1 evicted
		t.Errorf("v1 lookup kind=%v want stale", k)
	}
}
func TestBroadcastRejectRollsBack(t *testing.T) {
	// cap 10: the 16-byte snapshot exceeds even the V,V-1 floor, a 9-byte one fits.
	s, err := NewStore(10)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // rejected repeatedly; state must stay identical
		if _, err := s.Broadcast([]Entry{ent("abcdefgh", "x")}); !errors.Is(err, ErrSnapshotTooLarge) {
			t.Fatalf("got %v want ErrSnapshotTooLarge", err)
		}
		if s.V() != 0 || s.Used() != 0 || len(s.Versions()) != 1 || s.Versions()[0] != 0 {
			t.Fatalf("rejection left a trace: V=%d used=%d vs=%v", s.V(), s.Used(), s.Versions())
		}
	}
	// Still usable after rejection.
	if _, err := s.Broadcast([]Entry{ent("a", "1")}); err != nil {
		t.Fatalf("usable after reject: %v", err)
	}
	if s.V() != 1 || s.Used() != 9 {
		t.Fatalf("post-reject state V=%d used=%d", s.V(), s.Used())
	}
}
func TestLookupKinds(t *testing.T) {
	s, _ := NewStore(1 << 20)
	s.Broadcast([]Entry{ent("a", "1")}) // v1 = {a:1}
	s.Broadcast([]Entry{ent("b", "2")}) // v2 = copy of v1 plus b: {a:1,b:2}
	cases := []struct {
		vsn  int64
		key  string
		kind Kind
		val  string
	}{
		{1, "a", KindHit, "1"},
		{2, "a", KindHit, "1"}, // carried forward via the snapshot copy
		{2, "b", KindHit, "2"},
		{1, "b", KindMiss, ""}, // b did not exist in v1
		{0, "a", KindMiss, ""}, // v0 is the empty initial snapshot
		{3, "a", KindFuture, ""},
	}
	for _, c := range cases {
		if v, k := s.Lookup(c.vsn, c.key); k != c.kind || v != c.val {
			t.Errorf("Lookup(%d,%q)=%q,%v want %q,%v", c.vsn, c.key, v, k, c.val, c.kind)
		}
	}
}

// TestProbeCountBound proves map-based lookup: the unexported probe counter
// stays at an m-independent constant as the snapshot grows 100..10000.
func TestProbeCountBound(t *testing.T) {
	const bound = 2
	var prev int = -1
	for _, m := range []int{100, 1000, 10000} {
		s, _ := NewStore(1 << 40)
		batch := make([]Entry, m)
		for i := 0; i < m; i++ {
			batch[i] = ent(fmt.Sprintf("key-%05d", i), fmt.Sprint(i))
		}
		if _, err := s.Broadcast(batch); err != nil {
			t.Fatal(err)
		}
		target := fmt.Sprintf("key-%05d", m/2)
		if v, k := s.Lookup(1, target); k != KindHit || v != fmt.Sprint(m/2) {
			t.Fatalf("m=%d hit failed: %q %v", m, v, k)
		}
		if s.probe > bound {
			t.Errorf("m=%d probe=%d > bound %d (linear scan?)", m, s.probe, bound)
		}
		if prev >= 0 && s.probe != prev {
			t.Errorf("probe grew with m: %d -> %d", prev, s.probe)
		}
		prev = s.probe
	}
}
func TestSentinelDistinct(t *testing.T) {
	errs := []error{ErrEmptyKey, ErrEmptyBatch, ErrMaxBytes, ErrFuture, ErrSnapshotTooLarge}
	seen := map[error]bool{}
	for _, e := range errs {
		if seen[e] {
			t.Errorf("duplicate sentinel %v", e)
		}
		seen[e] = true
	}
	if _, err := NewStore(0); !errors.Is(err, ErrMaxBytes) {
		t.Errorf("NewStore(0)=%v want ErrMaxBytes", err)
	}
}
