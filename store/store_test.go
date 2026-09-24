package store_test

import (
	"fmt"
	"testing"

	"ontology/store"
)

func put(t *testing.T, s *store.Store, key, val string) {
	t.Helper()
	tx := s.Begin()
	if err := tx.Write(key, []byte(val)); err != nil {
		t.Fatalf("write %s: %v", key, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit %s: %v", key, err)
	}
}

func read(t *testing.T, s *store.Store, key string) (store.State, string) {
	t.Helper()
	snap, err := s.OpenSnapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer snap.Close()
	st, val := s.Get(snap, key)
	return st, string(val)
}

// buildHot returns a store with n single-version cold keys plus 5 hot
// keys with 10 versions each (45 shadowed candidates).
func buildHot(t *testing.T, n int) *store.Store {
	t.Helper()
	s := store.New(store.Options{})
	for i := 0; i < n; i++ {
		put(t, s, fmt.Sprintf("cold-%d", i), "x")
	}
	for v := 0; v < 10; v++ {
		for h := 0; h < 5; h++ {
			put(t, s, fmt.Sprintf("hot-%d", h), fmt.Sprintf("v%d", v))
		}
	}
	return s
}

func TestSnapshotIsolationRepeatableRead(t *testing.T) {
	s := store.New(store.Options{})
	put(t, s, "k", "v1")
	snap, err := s.OpenSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	put(t, s, "k", "v2")
	put(t, s, "other", "noise") // time passes, unrelated commits happen
	for i := 0; i < 3; i++ {
		st, val := s.Get(snap, "k")
		if st != store.Present || string(val) != "v1" {
			t.Fatalf("read %d: got %v %q, want v1", i, st, val)
		}
	}
	if st, val := read(t, s, "k"); st != store.Present || val != "v2" {
		t.Fatalf("fresh snapshot: got %v %q, want v2", st, val)
	}
}

func TestUncommittedAndReadOwnWrites(t *testing.T) {
	s := store.New(store.Options{})
	put(t, s, "k", "base")
	tx := s.Begin()
	if err := tx.Write("k", []byte("mine")); err != nil {
		t.Fatal(err)
	}
	if st, val := read(t, s, "k"); st != store.Present || val != "base" {
		t.Fatalf("other snapshot saw uncommitted: %v %q", st, val)
	}
	if st, val := tx.Get("k"); st != store.Present || string(val) != "mine" {
		t.Fatalf("read-own-writes: got %v %q", st, val)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if st, val := read(t, s, "k"); val != "mine" || st != store.Present {
		t.Fatalf("after commit: got %v %q", st, val)
	}
}

func TestRollbackNoResidue(t *testing.T) {
	s := store.New(store.Options{})
	tx := s.Begin()
	if err := tx.Write("gone", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if st, _ := read(t, s, "gone"); st != store.NeverExisted {
		t.Fatalf("rolled-back key visible: %v", st)
	}
	if got := s.Stats().TotalVersions; got != 0 {
		t.Fatalf("total versions = %d, want 0", got)
	}
	if got := s.KeyStats("gone"); got != 0 {
		t.Fatalf("key stats = %d, want 0", got)
	}
	if examined := s.Reclaim(); examined != 0 {
		t.Fatalf("reclaimer examined %d candidates from rolled-back tx", examined)
	}
}

func TestDeletedVsNeverExisted(t *testing.T) {
	s := store.New(store.Options{})
	put(t, s, "k", "v")
	before, err := s.OpenSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	tx := s.Begin()
	if err := tx.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		st   store.State
		want store.State
	}{
		{"deleted", func() store.State { st, _ := read(t, s, "k"); return st }(), store.Deleted},
		{"never", func() store.State { st, _ := read(t, s, "nope"); return st }(), store.NeverExisted},
		{"before-delete", func() store.State { st, _ := s.Get(before, "k"); return st }(), store.Present},
	}
	for _, c := range cases {
		if c.st != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.st, c.want)
		}
	}
}

func TestSnapshotPointBoundary(t *testing.T) {
	s := store.New(store.Options{})
	put(t, s, "before", "visible")
	snap, err := s.OpenSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	put(t, s, "at-point", "invisible") // its commit tx equals the snapshot point
	if st, _ := s.Get(snap, "at-point"); st != store.NeverExisted {
		t.Fatalf("commit at snapshot point visible: %v", st)
	}
	if st, val := s.Get(snap, "before"); st != store.Present || string(val) != "visible" {
		t.Fatalf("commit below snapshot point: got %v %q", st, val)
	}
}

func TestReclaimPreservesActiveSnapshotReads(t *testing.T) {
	s := store.New(store.Options{})
	keys := []string{"a", "b", "c"}
	put(t, s, "old", "g1")
	put(t, s, "old", "g2") // g1 is shadowed and reclaimable even under snap
	for _, k := range keys {
		put(t, s, k, "gen1-"+k)
	}
	snap, err := s.OpenSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	for gen := 2; gen <= 5; gen++ {
		for _, k := range keys {
			put(t, s, k, fmt.Sprintf("gen%d-%s", gen, k))
		}
	}
	watched := append([]string{"old"}, keys...)
	record := map[string]string{}
	for _, k := range watched {
		_, val := s.Get(snap, k)
		record[k] = string(val)
	}
	s.Reclaim()
	if got := s.KeyStats("old"); got != 1 {
		t.Fatalf("expected shadowed g1 reclaimed under active snapshot, chain len %d", got)
	}
	for _, k := range watched {
		st, val := s.Get(snap, k)
		if st != store.Present || string(val) != record[k] {
			t.Fatalf("key %s changed after reclaim: %v %q, want %q", k, st, val, record[k])
		}
	}
}
