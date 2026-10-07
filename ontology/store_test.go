package ontology

import (
	"errors"
	"testing"
)

func newTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	s := NewStore(opts...)
	s.RegisterType(ObjectType{
		Name: "Ticket",
		Properties: []PropertySpec{
			{Name: "title", Required: true},
			{Name: "state"},
			{Name: "tags", IsList: true, Min: 0, Max: 3},
		},
	})
	return s
}

func mustCreate(t *testing.T, s *Store, id InstanceID, props map[string]PropertyValue) {
	t.Helper()
	if err := s.CreateInstance(id, "Ticket", props); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func rejectCode(t *testing.T, err error) RejectCode {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	return re.Code
}

func TestOptimisticUpdateSuccess(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	v, err := s.Update("c1", "T1", 1, Mutation{Property: "title", Value: "b"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	snap, _ := s.Get("T1")
	if snap.Version != 2 || snap.Properties["title"] != "b" {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestOptimisticUpdateVersionStale(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	if _, err := s.Update("c1", "T1", 1, Mutation{Property: "title", Value: "b"}); err != nil {
		t.Fatalf("first update: %v", err)
	}
	_, err := s.Update("c2", "T1", 1, Mutation{Property: "title", Value: "c"})
	if code := rejectCode(t, err); code != RejectVersionStale {
		t.Fatalf("code = %s, want %s", code, RejectVersionStale)
	}
	snap, _ := s.Get("T1")
	if snap.Version != 2 || snap.Properties["title"] != "b" {
		t.Fatalf("rejected attempt changed state: %+v", snap)
	}
}

func TestOptimisticUpdateCardinality(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	_, err := s.Update("c1", "T1", 1,
		Mutation{Property: "tags", Value: []any{"x", "y", "z", "w"}})
	if code := rejectCode(t, err); code != RejectCardinality {
		t.Fatalf("code = %s, want %s", code, RejectCardinality)
	}
	snap, _ := s.Get("T1")
	if snap.Version != 1 {
		t.Fatalf("rejected attempt bumped version to %d", snap.Version)
	}
}

func TestUpdateDuringOccupancyRejected(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	occ, err := s.Acquire("A1", "T1")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	_, uerr := s.Update("c1", "T1", 1, Mutation{Property: "title", Value: "b"})
	if code := rejectCode(t, uerr); code != RejectInstanceOccupied {
		t.Fatalf("code = %s, want %s", code, RejectInstanceOccupied)
	}
	snap, _ := s.Get("T1")
	if snap.Version != 1 {
		t.Fatalf("occupied rejection bumped version to %d", snap.Version)
	}
	if err := occ.Abort(); err != nil {
		t.Fatalf("abort: %v", err)
	}
}

func TestOccupiedCheckPrecedesVersionCheck(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a"})

	occ, _ := s.Acquire("A1", "T1")
	// 期望版本故意写错：占用判定必须先于版本判定。
	_, err := s.Update("c1", "T1", 999, Mutation{Property: "title", Value: "b"})
	if code := rejectCode(t, err); code != RejectInstanceOccupied {
		t.Fatalf("code = %s, want %s", code, RejectInstanceOccupied)
	}
	occ.Abort()
}

func TestRetryAfterReleaseUsesLatestVersion(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, "T1", map[string]PropertyValue{"title": "a", "state": "open"})

	occ, _ := s.Acquire("A1", "T1")
	if err := occ.Apply(Mutation{Property: "state", Value: "closed"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// 占用期间用旧版本重试：拒绝且不改变版本。
	if _, err := s.Update("c1", "T1", 1, Mutation{Property: "title", Value: "b"}); err == nil {
		t.Fatal("update during occupancy should be rejected")
	}
	if _, err := occ.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// 释放后沿用占用开始前的旧版本：按版本落后拒绝。
	_, err := s.Update("c1", "T1", 1, Mutation{Property: "title", Value: "b"})
	if code := rejectCode(t, err); code != RejectVersionStale {
		t.Fatalf("code = %s, want %s", code, RejectVersionStale)
	}
	// 按释放后的最新版本重发：成功。
	v, err := s.Update("c1", "T1", 2, Mutation{Property: "title", Value: "b"})
	if err != nil || v != 3 {
		t.Fatalf("retry with latest version: v=%d err=%v", v, err)
	}
}

func TestRejectCodesDistinct(t *testing.T) {
	codes := []RejectCode{
		RejectOccupiedByAction,
		RejectInstanceOccupied,
		RejectVersionStale,
		RejectOrderConflict,
		RejectCardinality,
		RejectOccupancyLost,
	}
	seen := map[RejectCode]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Fatalf("duplicate code %v", c)
		}
		seen[c] = true
		if c.String() == "unknown" || c.String() == "none" {
			t.Fatalf("code %v has no distinct name", int(c))
		}
	}
}
