package ontology

import (
	"errors"
	"testing"
)

func TestStaleSnapshotAfterGC(t *testing.T) {
	m := newTestManager(t)
	snap := m.Acquire()
	mustUpdate(t, m, map[string]any{"host": "b"})
	snap.Release()
	m.GC() // reclaims v1, which snap pointed to
	if _, err := snap.Get("host"); !errors.Is(err, ErrVersionReclaimed) {
		t.Fatalf("Get on stale snapshot = %v, want ErrVersionReclaimed", err)
	}
	if _, err := snap.SourceVersion("host"); !errors.Is(err, ErrVersionReclaimed) {
		t.Fatalf("SourceVersion on stale snapshot = %v, want ErrVersionReclaimed", err)
	}
}

func TestReleasedSnapshotReadFails(t *testing.T) {
	m := newTestManager(t)
	snap := m.Acquire()
	snap.Release()
	if _, err := snap.Get("host"); !errors.Is(err, ErrSnapshotReleased) {
		t.Fatalf("Get after release = %v, want ErrSnapshotReleased", err)
	}
	if _, err := snap.SourceVersion("host"); !errors.Is(err, ErrSnapshotReleased) {
		t.Fatalf("SourceVersion after release = %v, want ErrSnapshotReleased", err)
	}
	// Version() stays readable even after release.
	if snap.Version() != 1 {
		t.Fatalf("Version() = %d, want 1", snap.Version())
	}
}

func TestStaleAndReleasedAreDistinctErrors(t *testing.T) {
	m := newTestManager(t)
	stale := m.Acquire()
	mustUpdate(t, m, map[string]any{"host": "b"})
	released := m.Acquire() // points to current version, never reclaimed
	stale.Release()
	released.Release()
	m.GC()
	_, staleErr := stale.Get("host")
	_, releasedErr := released.Get("host")
	if !errors.Is(staleErr, ErrVersionReclaimed) || errors.Is(staleErr, ErrSnapshotReleased) {
		t.Fatalf("stale error = %v, want ErrVersionReclaimed only", staleErr)
	}
	if !errors.Is(releasedErr, ErrSnapshotReleased) || errors.Is(releasedErr, ErrVersionReclaimed) {
		t.Fatalf("released error = %v, want ErrSnapshotReleased only", releasedErr)
	}
}

func TestDoubleReleaseIsIdempotent(t *testing.T) {
	m := newTestManager(t)
	snap := m.Acquire()
	snap.Release()
	snap.Release()
	snap.Release()
	rep := m.Leaks()
	if rep.Total != 0 {
		t.Fatalf("leaks after double release = %d, want 0", rep.Total)
	}
	// Refcount of v1 must be exactly 0, not negative: GC may reclaim it.
	mustUpdate(t, m, map[string]any{"host": "b"})
	got := m.GC()
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("GC reclaimed %v, want [1]", got)
	}
}

func TestLeakReport(t *testing.T) {
	m := newTestManager(t)
	s1 := m.Acquire()
	s2 := m.Acquire()
	mustUpdate(t, m, map[string]any{"host": "b"})
	s3 := m.Acquire()
	rep := m.Leaks()
	if rep.Total != 3 {
		t.Fatalf("leak total = %d, want 3", rep.Total)
	}
	if rep.ByVersion[1] != 2 || rep.ByVersion[2] != 1 {
		t.Fatalf("leak by version = %v, want {1:2, 2:1}", rep.ByVersion)
	}
	s1.Release()
	s2.Release()
	rep = m.Leaks()
	if rep.Total != 1 || rep.ByVersion[2] != 1 {
		t.Fatalf("after releases leaks = %+v, want total 1 at v2", rep)
	}
	s3.Release()
	if rep := m.Leaks(); rep.Total != 0 {
		t.Fatalf("final leaks = %+v, want 0", rep)
	}
}

func TestGetUnknownFieldFails(t *testing.T) {
	m := newTestManager(t)
	snap := m.Acquire()
	defer snap.Release()
	_, err := snap.Get("nope")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Kind != KindUnknownField {
		t.Fatalf("Get unknown field = %v, want ValidationError/unknown", err)
	}
}
