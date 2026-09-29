package logstore

import (
	"errors"
	"log"
	"strings"
	"testing"
)

// testLogger routes the store's decision log into the test log so every
// input, output and decision rationale shows up in `go test -v` output.
type testLogger struct{ t *testing.T }

func (l testLogger) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func newStore(t *testing.T, segSize, maxSegs int) *Store {
	t.Helper()
	s, err := New(Config{
		SegmentSize: segSize,
		MaxSegments: maxSegs,
		Logger:      log.New(testLogger{t}, "", 0),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func putBytes(t *testing.T, s *Store, key string, valueLen int) {
	t.Helper()
	t.Logf("input: Put(%q, %d bytes)", key, valueLen)
	if err := s.Put(key, make([]byte, valueLen)); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func del(t *testing.T, s *Store, key string) {
	t.Helper()
	t.Logf("input: Delete(%q)", key)
	if err := s.Delete(key); err != nil {
		t.Fatalf("Delete(%q): %v", key, err)
	}
}

func mustVerify(t *testing.T, s *Store) {
	t.Helper()
	if err := s.VerifyAccounting(); err != nil {
		t.Fatalf("accounting invariant violated: %v", err)
	}
}

// findBlock locates a block by key and tombstone flag across all segments.
func findBlock(s *Store, key string, tombstone bool) (SegmentInfo, BlockInfo, bool) {
	for _, seg := range s.Segments() {
		for _, b := range seg.Blocks {
			if b.Key == key && b.Tombstone == tombstone {
				return seg, b, true
			}
		}
	}
	return SegmentInfo{}, BlockInfo{}, false
}

func TestRejections(t *testing.T) {
	s := newStore(t, 52, 2)
	before := s.Dump()

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"empty key put", func() error { return s.Put("", []byte("v")) }, ErrEmptyKey},
		{"empty key delete", func() error { return s.Delete("") }, ErrEmptyKey},
		{"oversize block", func() error { return s.Put("k", make([]byte, 100)) }, ErrBlockTooLarge},
		{"delete missing key", func() error { return s.Delete("nope") }, ErrKeyNotFound},
	}
	for _, c := range cases {
		err := c.op()
		t.Logf("input: %s -> output: %v", c.name, err)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
	}

	putBytes(t, s, "k1", 8)
	del(t, s, "k1")
	if err := s.Delete("k1"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("double delete: got %v, want %v", err, ErrKeyNotFound)
	}
	t.Log("input: double delete -> output: ErrKeyNotFound (tombstone is not a value)")

	if got := s.Dump(); got == before {
		t.Fatal("dump should change after successful ops")
	}
	mustVerify(t, s)
}

// TestCostBenefitTie builds two sealed segments whose cost-benefit scores are
// exactly equal as rationals and checks the tie breaks to the lowest slot id.
//
// SegmentSize=100, block size=20 (16 header + 1 key + 3 value).
//   - slot0: a..e (ts 1-5), a,b overwritten later  -> live 60, maxTS 5
//   - slot1: f..j (ts 6-10), f,g,h overwritten     -> live 40, maxTS 10
//   - slot2: overwrites a,b,f,g,h (ts 11-15)       -> live 100
//   - slot3: x,y (ts 16-17), current, remaining 60
//
// At clock 17: score(slot0) = (100-60)*12/(100+60) = 3,
// score(slot1) = (100-40)*7/(100+40) = 3. Tie -> victim must be slot0.
func TestCostBenefitTie(t *testing.T) {
	s := newStore(t, 100, 4)
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		putBytes(t, s, k, 3)
	}
	for _, k := range []string{"a", "b", "f", "g", "h"} { // overwrite: kill 2 in slot0, 3 in slot1
		putBytes(t, s, k, 3)
	}
	putBytes(t, s, "x", 3)
	putBytes(t, s, "y", 3)

	victim := s.pickVictim()
	t.Logf("decision: victim=%d (tie 3 == 3, lowest id wins)", victim.id)
	if victim.id != 0 {
		t.Fatalf("tie-break: got victim %d, want 0", victim.id)
	}

	// Trigger the clean: 61-byte block does not fit the 60 remaining bytes.
	putBytes(t, s, "big", 42) // size = 16+3+42 = 61
	mustVerify(t, s)

	segs := s.Segments()
	t.Logf("output layout:\n%s", s.Dump())
	// slot0 was reclaimed and reused as the new current segment holding "big".
	if segs[0].ID != 0 || len(segs[0].Blocks) != 1 || segs[0].Blocks[0].Key != "big" {
		t.Fatalf("slot0 should hold only big, got %+v", segs[0])
	}
	// slot3 (old current) received the migrated live blocks c,d,e in order.
	var keys []string
	for _, b := range segs[3].Blocks {
		keys = append(keys, b.Key)
	}
	if got, want := strings.Join(keys, ","), "x,y,c,d,e"; got != want {
		t.Fatalf("slot3 blocks = %s, want %s", got, want)
	}
	// slot1 (the other tied candidate) must be untouched.
	if segs[1].ID != 1 || len(segs[1].Blocks) != 5 {
		t.Fatalf("slot1 should be untouched, got %+v", segs[1])
	}
}

// TestZeroLiveReclaim checks that a segment with zero live bytes is reclaimed
// directly, without migration, even when the tail has no room at all.
func TestZeroLiveReclaim(t *testing.T) {
	s := newStore(t, 52, 2) // block size = 16+2+8 = 26, two blocks per segment
	putBytes(t, s, "k1", 8)
	putBytes(t, s, "k2", 8)
	putBytes(t, s, "k1", 8) // overwrite: slot0 now fully dead
	putBytes(t, s, "k2", 8)

	putBytes(t, s, "k3", 8) // forces clean of slot0 (live 0) then reuse
	mustVerify(t, s)
	t.Logf("output layout:\n%s", s.Dump())

	segs := s.Segments()
	if len(segs) != 2 {
		t.Fatalf("want 2 occupied slots, got %d", len(segs))
	}
	if segs[0].ID != 0 || len(segs[0].Blocks) != 1 || segs[0].Blocks[0].Key != "k3" {
		t.Fatalf("slot0 should be reused for k3, got %+v", segs[0])
	}
	if _, ok := s.Get("k1"); !ok {
		t.Fatal("k1 (overwritten value in slot1) must still be readable")
	}
}
