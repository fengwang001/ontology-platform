package ontology

import (
	"reflect"
	"testing"
)

func testDecls() []FieldDecl {
	return []FieldDecl{
		{Name: "host", Type: StringField},
		{Name: "port", Type: IntField, Min: 1, Max: 65535},
		{Name: "retries", Type: IntField, Min: 0, Max: 10},
	}
}

func testInitial() map[string]any {
	return map[string]any{"host": "a", "port": 80, "retries": 3}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(testDecls(), testInitial())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func mustUpdate(t *testing.T, m *Manager, changes map[string]any) int64 {
	t.Helper()
	v, err := m.Update(changes)
	if err != nil {
		t.Fatalf("Update(%v): %v", changes, err)
	}
	return v
}

func mustGet(t *testing.T, s *Snapshot, name string) any {
	t.Helper()
	v, err := s.Get(name)
	if err != nil {
		t.Fatalf("Get(%q): %v", name, err)
	}
	return v
}

func TestSnapshotIsolatedFromLaterUpdates(t *testing.T) {
	m := newTestManager(t)
	snap := m.Acquire()
	defer snap.Release()
	mustUpdate(t, m, map[string]any{"host": "b", "port": 8080})
	mustUpdate(t, m, map[string]any{"host": "c", "retries": 9})
	if got := mustGet(t, snap, "host"); got != "a" {
		t.Fatalf("host = %v, want a", got)
	}
	if got := mustGet(t, snap, "port"); got != int64(80) {
		t.Fatalf("port = %v, want 80", got)
	}
	if got := mustGet(t, snap, "retries"); got != int64(3) {
		t.Fatalf("retries = %v, want 3", got)
	}
	if snap.Version() != 1 {
		t.Fatalf("snapshot version = %d, want 1", snap.Version())
	}
}

func TestFieldSourceVersions(t *testing.T) {
	m := newTestManager(t)
	v2 := mustUpdate(t, m, map[string]any{"host": "b"})
	v3 := mustUpdate(t, m, map[string]any{"port": 8080, "retries": 5})
	snap := m.Acquire()
	defer snap.Release()
	cases := map[string]int64{"host": v2, "port": v3, "retries": v3}
	for name, want := range cases {
		got, err := snap.SourceVersion(name)
		if err != nil {
			t.Fatalf("SourceVersion(%q): %v", name, err)
		}
		if got != want {
			t.Fatalf("SourceVersion(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestSameValueRewriteKeepsSourceVersion(t *testing.T) {
	m := newTestManager(t)
	v2 := mustUpdate(t, m, map[string]any{"host": "b"})
	mustUpdate(t, m, map[string]any{"host": "b"}) // same value: source stays
	snap := m.Acquire()
	defer snap.Release()
	got, err := snap.SourceVersion("host")
	if err != nil {
		t.Fatal(err)
	}
	if got != v2 {
		t.Fatalf("source = %d, want %d (unchanged on same-value rewrite)", got, v2)
	}
}

func TestSwitchToHistoryProducesLargerVersion(t *testing.T) {
	m := newTestManager(t)
	mustUpdate(t, m, map[string]any{"host": "b"})
	cur := m.CurrentVersion()
	nv, err := m.SwitchTo(1)
	if err != nil {
		t.Fatal(err)
	}
	if nv <= cur {
		t.Fatalf("SwitchTo version = %d, want > %d", nv, cur)
	}
	c, err := m.ContentAt(nv)
	if err != nil {
		t.Fatal(err)
	}
	if c["host"] != "a" {
		t.Fatalf("switched content host = %v, want a", c["host"])
	}
	// Historical content is still available afterwards, unchanged.
	h, err := m.ContentAt(1)
	if err != nil {
		t.Fatal(err)
	}
	if h["host"] != "a" || h["port"] != int64(80) {
		t.Fatalf("history v1 = %v, want original", h)
	}
	// Version numbers keep advancing after a switch, never reused.
	v4 := mustUpdate(t, m, map[string]any{"host": "z"})
	if v4 <= nv {
		t.Fatalf("post-switch update version = %d, want > %d", v4, nv)
	}
}

func TestGCRespectsReferences(t *testing.T) {
	m := newTestManager(t)
	snap := m.Acquire() // pins v1
	mustUpdate(t, m, map[string]any{"host": "b"})
	v3 := mustUpdate(t, m, map[string]any{"host": "c"})
	// v1 is pinned by snap, v3 is current: only v2 may be reclaimed.
	if got := m.GC(); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("GC with pinned v1 reclaimed %v, want [2]", got)
	}
	if _, err := m.ContentAt(1); err != nil {
		t.Fatalf("pinned v1 must survive GC: %v", err)
	}
	snap.Release()
	got := m.GC()
	want := []int64{1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GC reclaimed %v, want %v", got, want)
	}
	if got := m.GC(); len(got) != 0 {
		t.Fatalf("second GC reclaimed %v, want nothing", got)
	}
	if m.CurrentVersion() != v3 {
		t.Fatalf("current = %d, want %d", m.CurrentVersion(), v3)
	}
	if _, err := m.ContentAt(v3); err != nil {
		t.Fatalf("current version must never be reclaimed: %v", err)
	}
}

func TestContentAtReclaimedVersionFails(t *testing.T) {
	m := newTestManager(t)
	mustUpdate(t, m, map[string]any{"host": "b"})
	m.GC()
	if _, err := m.ContentAt(1); err == nil {
		t.Fatal("ContentAt on reclaimed version should fail")
	}
}
