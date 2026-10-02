package hashdir

import (
	"errors"
	"math"
	"testing"
)

func TestReadDirPosInclusive(t *testing.T) {
	s := mustStore(t, math.MaxInt32)
	k0 := mustAdd(t, s, collNames[0], 1)
	k1 := mustAdd(t, s, collNames[1], 2)
	// A cursor whose Pos equals an item's key includes that item.
	ents, next, done, err := s.ReadDir(Cookie{Gen: 1, Pos: k1}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Key != k1 || ents[0].Name != collNames[1] {
		t.Fatalf("entries = %+v, want exactly the item at Pos", ents)
	}
	if next.Pos != k1+1 || next.Gen != 1 {
		t.Fatalf("next = %+v", next)
	}
	if !done {
		t.Fatal("Done = false, want true (no key >= k1+1)")
	}
	// Pos exactly k0 returns both.
	ents, _, done, err = s.ReadDir(Cookie{Gen: 1, Pos: k0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 || !done {
		t.Fatalf("entries = %+v done = %v", ents, done)
	}
}

func TestPagingVisibility(t *testing.T) {
	s := mustStore(t, math.MaxInt32)
	ka0 := mustAdd(t, s, collNames[0], 1)
	kp0 := mustAdd(t, s, pairNames[0], 2)
	// Read the first item only; the cursor sits between the two keys.
	first, second := collNames[0], pairNames[0]
	kFirst, kSecond := ka0, kp0
	if ka0 > kp0 {
		first, second, kFirst, kSecond = pairNames[0], collNames[0], kp0, ka0
	}
	ents, c, done, err := s.ReadDir(Cookie{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name != first || done {
		t.Fatalf("first page = %+v done = %v", ents, done)
	}
	if c.Pos != kFirst+1 {
		t.Fatalf("cursor pos = %d, want %d", c.Pos, kFirst+1)
	}
	// Add one item before the cursor (reusing the freed minor of the first
	// group) and one after it.
	if err := s.Remove(first); err != nil {
		t.Fatal(err)
	}
	var before string
	if first == collNames[0] {
		before = collNames[1]
	} else {
		before = pairNames[1]
	}
	kBefore := mustAdd(t, s, before, 3)
	if kBefore != kFirst {
		t.Fatalf("before-item key = %d, want reused %d (< cursor)", kBefore, kFirst)
	}
	var after string
	if second == pairNames[0] {
		after = pairNames[1]
	} else {
		after = collNames[1]
	}
	kAfter := mustAdd(t, s, after, 4)
	if kAfter <= kSecond {
		t.Fatalf("after-item key = %d, want > %d", kAfter, kSecond)
	}
	// Resume: the before-cursor item is invisible, both later items appear.
	ents, c, done, err = s.ReadDir(c, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 || ents[0].Key != kSecond || ents[1].Key != kAfter {
		t.Fatalf("resumed entries = %+v, want keys [%d %d]", ents, kSecond, kAfter)
	}
	if !done {
		t.Fatal("Done = false after draining")
	}
	_ = c
}

func TestRehashMigrationExample(t *testing.T) {
	s := mustStore(t, math.MaxInt32)
	// collNames is byte-sorted: p < q < r.
	p, q, r := collNames[0], collNames[1], collNames[2]
	kr := mustAdd(t, s, r, 1)
	mustAdd(t, s, p, 2)
	mustAdd(t, s, q, 3)
	if minorOf(kr) != 0 {
		t.Fatalf("r minor = %d, want 0 (added first)", minorOf(kr))
	}
	c, err := s.CookieOf(r)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Cookie{Gen: 1, Pos: kr + 1}) {
		t.Fatalf("cookie = %+v", c)
	}
	// Rehash with the same seed still bumps the generation and reassigns
	// minors in byte-wise name order: p=0, q=1, r=2.
	if err := s.Rehash(collSeed); err != nil {
		t.Fatal(err)
	}
	if s.Gen() != 2 {
		t.Fatalf("gen = %d, want 2", s.Gen())
	}
	cr := mustCookieOf(t, s, r)
	if minorOf(cr.Pos-1) != 2 {
		t.Fatalf("r minor after rehash = %d, want 2", minorOf(cr.Pos-1))
	}
	// The old cookie migrates via Pos-1 == snapshot key of r.
	ents, nc, done, err := s.ReadDir(c, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 || !done {
		t.Fatalf("entries = %+v done = %v, want empty + Done", ents, done)
	}
	if nc != cr {
		t.Fatalf("migrated cursor = %+v, want %+v", nc, cr)
	}
	// After removing r, the same old cookie is stale.
	if err := s.Remove(r); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ReadDir(c, 10); !errors.Is(err, ErrStaleCookie) {
		t.Fatalf("after Remove(r): want ErrStaleCookie, got %v", err)
	}
	// A second rehash makes generation-1 cookies too old.
	if err := s.Rehash(collSeed); err != nil {
		t.Fatal(err)
	}
	if s.Gen() != 3 {
		t.Fatalf("gen = %d, want 3", s.Gen())
	}
	if _, _, _, err := s.ReadDir(c, 10); !errors.Is(err, ErrStaleCookie) {
		t.Fatalf("gen-1 cookie at gen 3: want ErrStaleCookie, got %v", err)
	}
}

func TestMigrationRequiresSnapshotKey(t *testing.T) {
	s := mustStore(t, math.MaxInt32)
	k0 := mustAdd(t, s, collNames[0], 1)
	if err := s.Rehash(collSeed); err != nil {
		t.Fatal(err)
	}
	// Pos-1 is not any snapshot key: stale.
	bogus := Cookie{Gen: 1, Pos: k0 + 2}
	if _, _, _, err := s.ReadDir(bogus, 10); !errors.Is(err, ErrStaleCookie) {
		t.Fatalf("Pos-1 not a snapshot key: want ErrStaleCookie, got %v", err)
	}
	// Pos-1 equal to the snapshot key migrates.
	ents, _, _, err := s.ReadDir(Cookie{Gen: 1, Pos: k0 + 1}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("entries = %+v, want empty (migrated past the only item)", ents)
	}
}

func TestPosZeroAlwaysUsable(t *testing.T) {
	s := mustStore(t, math.MaxInt32)
	mustAdd(t, s, collNames[0], 1)
	if err := s.Rehash(collSeed); err != nil {
		t.Fatal(err)
	}
	if err := s.Rehash(collSeed); err != nil {
		t.Fatal(err)
	}
	// Pos == 0 skips the generation check entirely, even with a nonsense gen.
	for _, gen := range []uint64{0, 1, 2, 3, 9999} {
		ents, _, _, err := s.ReadDir(Cookie{Gen: gen, Pos: 0}, 10)
		if err != nil {
			t.Fatalf("gen %d Pos 0: %v", gen, err)
		}
		if len(ents) != 1 {
			t.Fatalf("gen %d: entries = %+v", gen, ents)
		}
	}
}

func TestReadDirZeroN(t *testing.T) {
	s := mustStore(t, math.MaxInt32)
	k0 := mustAdd(t, s, collNames[0], 1)
	// n == 0 from the start: empty list, cursor unchanged, Done false.
	ents, next, done, err := s.ReadDir(Cookie{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ents != nil || next != (Cookie{}) || done {
		t.Fatalf("ents=%v next=%+v done=%v", ents, next, done)
	}
	// n == 0 past the last item: Done true.
	_, next, done, err = s.ReadDir(Cookie{Gen: 1, Pos: k0 + 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if next != (Cookie{Gen: 1, Pos: k0 + 1}) || !done {
		t.Fatalf("next=%+v done=%v", next, done)
	}
	// n < 0 is rejected.
	if _, _, _, err := s.ReadDir(Cookie{}, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("n=-1: want ErrInvalidArgument, got %v", err)
	}
}

func TestRehashOverflowRejected(t *testing.T) {
	// Two items under distinct hashes (seed 1) fit with M=1, but rehashing
	// to collSeed where they collide must be rejected without changing gen
	// or snapshot.
	s, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	kc := mustAdd(t, s, collNames[0], 1)
	kp := mustAdd(t, s, collNames[1], 2)
	genBefore := s.Gen()
	if err := s.Rehash(collSeed); !errors.Is(err, ErrHashFull) {
		t.Fatalf("rehash into collision: want ErrHashFull, got %v", err)
	}
	if s.Gen() != genBefore {
		t.Fatalf("gen changed by rejected rehash: %d -> %d", genBefore, s.Gen())
	}
	// Keys unchanged.
	if c := mustCookieOf(t, s, collNames[0]); c.Pos != kc+1 {
		t.Fatalf("coll key changed: %+v", c)
	}
	if c := mustCookieOf(t, s, collNames[1]); c.Pos != kp+1 {
		t.Fatalf("pair key changed: %+v", c)
	}
	// Snapshot unchanged: there was none before, so a gen-1 cookie (there is
	// no older generation) must still be stale rather than migratable.
	if _, _, _, err := s.ReadDir(Cookie{Gen: genBefore - 1, Pos: kc + 1}, 1); !errors.Is(err, ErrStaleCookie) {
		t.Fatalf("want ErrStaleCookie, got %v", err)
	}
}

func TestRehashOverflowKeepsSnapshot(t *testing.T) {
	// A successful rehash followed by a rejected one keeps the first
	// snapshot for migration.
	s, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	k0 := mustAdd(t, s, collNames[0], 1)
	mustAdd(t, s, collNames[1], 2)
	// Seed 2 keeps them in distinct buckets: rehash succeeds, gen -> 2.
	if hashName(2, collNames[0]) == hashName(2, collNames[1]) {
		t.Fatal("test assumption broken: seed 2 collides")
	}
	if err := s.Rehash(2); err != nil {
		t.Fatal(err)
	}
	// collSeed makes them collide with M=1 -> rejected; gen/snapshot stay.
	if err := s.Rehash(collSeed); !errors.Is(err, ErrHashFull) {
		t.Fatalf("want ErrHashFull, got %v", err)
	}
	if s.Gen() != 2 {
		t.Fatalf("gen = %d, want 2", s.Gen())
	}
	// The generation-1 cookie still migrates through the preserved snapshot.
	ents, _, _, err := s.ReadDir(Cookie{Gen: 1, Pos: k0 + 1}, 10)
	if err != nil {
		t.Fatalf("migration after rejected rehash: %v", err)
	}
	if len(ents) != 1 || ents[0].Name != collNames[1] {
		t.Fatalf("entries = %+v", ents)
	}
}

func TestCookieOfRejects(t *testing.T) {
	s := mustStore(t, 10)
	if _, err := s.CookieOf(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty: want ErrInvalidArgument, got %v", err)
	}
	if _, err := s.CookieOf("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: want ErrNotFound, got %v", err)
	}
}

func TestSameSeedRehashReordersMinors(t *testing.T) {
	s := mustStore(t, math.MaxInt32)
	// Insert in scrambled order so minors follow insertion, not name order.
	mustAdd(t, s, collNames[2], 1)
	mustAdd(t, s, collNames[0], 2)
	mustAdd(t, s, collNames[1], 3)
	if err := s.Rehash(collSeed); err != nil {
		t.Fatal(err)
	}
	for i, name := range collNames {
		c := mustCookieOf(t, s, name)
		if minorOf(c.Pos-1) != uint32(i) {
			t.Fatalf("%s minor = %d, want %d (byte order)", name, minorOf(c.Pos-1), i)
		}
	}
}

func TestStaleCookieFutureGen(t *testing.T) {
	s := mustStore(t, 10)
	mustAdd(t, s, collNames[0], 1)
	if _, _, _, err := s.ReadDir(Cookie{Gen: 5, Pos: 1}, 1); !errors.Is(err, ErrStaleCookie) {
		t.Fatalf("future gen: want ErrStaleCookie, got %v", err)
	}
}
