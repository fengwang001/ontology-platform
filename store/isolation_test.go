package store

import (
	"bytes"
	"testing"

	"ontology/version"
)

func commitPut(t *testing.T, s *Store, key, val string) {
	t.Helper()
	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put(key, []byte(val)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Semantics 1: snapshot isolation + repeatable read.
func TestSnapshotIsolationRepeatable(t *testing.T) {
	s := New(Config{})
	commitPut(t, s, "k", "v1")

	rd, err := s.BeginRead()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	before := rd.Get("k")
	commitPut(t, s, "k", "v2")
	commitPut(t, s, "k", "v3")
	for i := 0; i < 3; i++ {
		r := rd.Get("k")
		if r.Outcome != before.Outcome || !bytes.Equal(r.Value, before.Value) {
			t.Fatalf("repeat read changed: %q vs %q", r.Value, before.Value)
		}
	}
	if got := rd.Get("k"); got.Outcome != version.Present ||
		string(got.Value) != "v1" {
		t.Fatalf("snapshot saw %v %q, want v1", got.Outcome, got.Value)
	}
}

// Semantics 2: uncommitted invisible to others, visible to self.
func TestUncommittedInvisibleReadYourWrites(t *testing.T) {
	s := New(Config{})
	commitPut(t, s, "k", "v1")

	other, _ := s.BeginRead()
	defer other.Close()

	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put("k", []byte("draft")); err != nil {
		t.Fatal(err)
	}
	r := tx.Get("k")
	if r.Outcome != version.Present || string(r.Value) != "draft" {
		t.Fatalf("read-your-writes = %v %q", r.Outcome, r.Value)
	}
	r = other.Get("k")
	if r.Outcome != version.Present || string(r.Value) != "v1" {
		t.Fatalf("other saw uncommitted: %v %q", r.Outcome, r.Value)
	}
}

// Semantics 2: rollback leaves nothing visible.
func TestRollbackNoResidue(t *testing.T) {
	s := New(Config{})
	commitPut(t, s, "k", "v1")

	tx, _ := s.Begin()
	if err := tx.Put("k", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if s.TotalVersions() != 1 {
		t.Fatalf("total after rollback = %d, want 1", s.TotalVersions())
	}
	rd, _ := s.BeginRead()
	defer rd.Close()
	if r := rd.Get("k"); string(r.Value) != "v1" {
		t.Fatalf("post-rollback read = %q", r.Value)
	}
	if r := rd.Get("ghost"); r.Outcome != version.Absent {
		t.Fatalf("never-existed = %v", r.Outcome)
	}
}

// Semantics 3: delete vs never-existed are distinguishable.
func TestDeleteVersusNeverExisted(t *testing.T) {
	s := New(Config{})
	commitPut(t, s, "k", "v1")
	tx, _ := s.Begin()
	if err := tx.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rd, _ := s.BeginRead()
	defer rd.Close()
	if r := rd.Get("k"); r.Outcome != version.Deleted {
		t.Fatalf("deleted key = %v, want Deleted", r.Outcome)
	}
	if r := rd.Get("nope"); r.Outcome != version.Absent {
		t.Fatalf("never key = %v, want Absent", r.Outcome)
	}
}

// Semantics 4: commit exactly at the snapshot point is invisible.
func TestBoundaryEqualSnapshotPoint(t *testing.T) {
	s := New(Config{})
	commitPut(t, s, "seed", "x") // txid 1 committed; next id is 2
	rd, _ := s.BeginRead()
	if rd.snap.Point != 2 {
		t.Fatalf("reader point = %d, want 2", rd.snap.Point)
	}

	// A transaction starting *after* the snapshot gets id 2 and commits at 2:
	// cid == point, right-open => invisible.
	tx, _ := s.Begin()
	if err := tx.Put("k", []byte("at2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if r := rd.Get("k"); r.Outcome != version.Absent {
		t.Fatalf("commit exactly at point visible: %v", r.Outcome)
	}
	rd.Close()
	// A fresh snapshot sees it.
	fresh, _ := s.BeginRead()
	defer fresh.Close()
	if r := fresh.Get("k"); string(r.Value) != "at2" {
		t.Fatalf("fresh snapshot = %q", r.Value)
	}
}
