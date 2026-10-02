package hashdir

import (
	"errors"
	"math"
	"testing"
)

func TestNewValidatesM(t *testing.T) {
	for _, m := range []uint32{0, math.MaxInt32 + 1, math.MaxUint32} {
		if _, err := New(1, m); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(1, %d): want ErrInvalidArgument, got %v", m, err)
		}
	}
	for _, m := range []uint32{1, 2, math.MaxInt32} {
		if _, err := New(1, m); err != nil {
			t.Fatalf("New(1, %d): %v", m, err)
		}
	}
	s, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if s.Gen() != 1 {
		t.Fatalf("initial gen = %d, want 1", s.Gen())
	}
}

func TestMinorSmallestFreeAndReuse(t *testing.T) {
	s := mustStore(t, math.MaxInt32)
	k0 := mustAdd(t, s, collNames[0], 10)
	k1 := mustAdd(t, s, collNames[1], 11)
	k2 := mustAdd(t, s, collNames[2], 12)
	if minorOf(k0) != 0 || minorOf(k1) != 1 || minorOf(k2) != 2 {
		t.Fatalf("minors = %d,%d,%d, want 0,1,2", minorOf(k0), minorOf(k1), minorOf(k2))
	}
	if hashOf(k0) != hashOf(k1) || hashOf(k1) != hashOf(k2) {
		t.Fatal("collision group hashes differ")
	}
	// Deleting the middle item frees minor 1, reused by the next add.
	if err := s.Remove(collNames[1]); err != nil {
		t.Fatal(err)
	}
	k1b := mustAdd(t, s, collNames[1], 13)
	if minorOf(k1b) != 1 {
		t.Fatalf("re-added minor = %d, want 1 (smallest free)", minorOf(k1b))
	}
	// Freeing minor 0 and 2 makes 0 the smallest free again.
	if err := s.Remove(collNames[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(collNames[2]); err != nil {
		t.Fatal(err)
	}
	k0b := mustAdd(t, s, collNames[0], 14)
	if minorOf(k0b) != 0 {
		t.Fatalf("re-added minor = %d, want 0", minorOf(k0b))
	}
}

func TestHashFullAtM(t *testing.T) {
	s := mustStore(t, 2)
	mustAdd(t, s, collNames[0], 1)
	mustAdd(t, s, collNames[1], 2)
	if _, err := s.Add(collNames[2], 3); !errors.Is(err, ErrHashFull) {
		t.Fatalf("3rd same-hash add: want ErrHashFull, got %v", err)
	}
	// A different-hash name still fits.
	mustAdd(t, s, pairNames[0], 4)
	// State unchanged by the rejection: the colliding name can still be
	// added after freeing a slot.
	if err := s.Remove(collNames[1]); err != nil {
		t.Fatal(err)
	}
	k := mustAdd(t, s, collNames[2], 5)
	if minorOf(k) != 1 {
		t.Fatalf("minor = %d, want 1 (freed slot)", minorOf(k))
	}
}

func TestAddRejects(t *testing.T) {
	s := mustStore(t, 10)
	if _, err := s.Add("", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty name: want ErrInvalidArgument, got %v", err)
	}
	mustAdd(t, s, "a", 1)
	if _, err := s.Add("a", 2); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup name: want ErrAlreadyExists, got %v", err)
	}
}

func TestRemoveRejects(t *testing.T) {
	s := mustStore(t, 10)
	if err := s.Remove(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty name: want ErrInvalidArgument, got %v", err)
	}
	if err := s.Remove("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing name: want ErrNotFound, got %v", err)
	}
}

func TestRenameSameHashReusesMinor(t *testing.T) {
	// M=1: the bucket is exactly full, yet a same-hash rename succeeds
	// because old is deleted before the fullness check.
	s := mustStore(t, 1)
	k0 := mustAdd(t, s, collNames[0], 42)
	k1, err := s.Rename(collNames[0], collNames[1])
	if err != nil {
		t.Fatalf("same-hash rename with full bucket: %v", err)
	}
	if k1 != k0 {
		t.Fatalf("key = %d, want reused %d (same minor)", k1, k0)
	}
	ents, _, _, err := s.ReadDir(Cookie{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name != collNames[1] || ents[0].Ino != 42 {
		t.Fatalf("entries = %+v, want renamed item with ino preserved", ents)
	}
}

func TestRenameToFullHashFails(t *testing.T) {
	s := mustStore(t, 1)
	kc := mustAdd(t, s, collNames[0], 1)
	kp := mustAdd(t, s, pairNames[0], 2)
	if _, err := s.Rename(pairNames[0], collNames[1]); !errors.Is(err, ErrHashFull) {
		t.Fatalf("rename into full hash: want ErrHashFull, got %v", err)
	}
	// Rejection changed nothing.
	if got := mustCookieOf(t, s, collNames[0]); got.Pos != kc+1 {
		t.Fatalf("coll item moved: %+v", got)
	}
	if got := mustCookieOf(t, s, pairNames[0]); got.Pos != kp+1 {
		t.Fatalf("pair item moved: %+v", got)
	}
	if _, err := s.CookieOf(collNames[1]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("phantom rename target: want ErrNotFound, got %v", err)
	}
}

func TestRenameRejects(t *testing.T) {
	s := mustStore(t, 10)
	mustAdd(t, s, "a", 1)
	mustAdd(t, s, "b", 2)
	cases := []struct {
		old, new string
		want     error
	}{
		{"", "x", ErrInvalidArgument},
		{"a", "", ErrInvalidArgument},
		{"ghost", "x", ErrNotFound},
		{"a", "a", ErrAlreadyExists},
		{"a", "b", ErrAlreadyExists},
	}
	for _, tc := range cases {
		if _, err := s.Rename(tc.old, tc.new); !errors.Is(err, tc.want) {
			t.Fatalf("Rename(%q,%q): want %v, got %v", tc.old, tc.new, tc.want, err)
		}
	}
	// Ino is preserved across a successful rename.
	if _, err := s.Rename("a", "c"); err != nil {
		t.Fatal(err)
	}
	ents, _, _, err := s.ReadDir(Cookie{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name == "c" && e.Ino != 1 {
			t.Fatalf("ino not preserved: %+v", e)
		}
	}
}

func mustCookieOf(t *testing.T, s *Store, name string) Cookie {
	t.Helper()
	c, err := s.CookieOf(name)
	if err != nil {
		t.Fatalf("CookieOf(%q): %v", name, err)
	}
	return c
}
