package ontology

import (
	"testing"
)

// sourcesOf acquires a snapshot pinned to the current version and reads
// the source version of every declared field.
func sourcesOf(t *testing.T, m *Manager) map[string]int64 {
	t.Helper()
	snap := m.Acquire()
	defer snap.Release()
	out := make(map[string]int64)
	for _, d := range testDecls() {
		v, err := snap.SourceVersion(d.Name)
		if err != nil {
			t.Fatalf("SourceVersion(%q): %v", d.Name, err)
		}
		out[d.Name] = v
	}
	return out
}

func mustContent(t *testing.T, m *Manager, version int64) map[string]any {
	t.Helper()
	c, err := m.ContentAt(version)
	if err != nil {
		t.Fatalf("ContentAt(%d): %v", version, err)
	}
	return c
}

func equalMaps[K comparable, V comparable](a, b map[K]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// TestUpdateNoOpForms pins the asymmetry between an empty update and a
// non-empty update whose values are all identical to the current ones:
// the former is a no-op, the latter still advances the version number and
// clones a field-by-field identical version.
func TestUpdateNoOpForms(t *testing.T) {
	cases := []struct {
		name          string
		changes       map[string]any
		wantVersion   int64
		wantCurrent   int64
		wantNewCloned bool // whether a new, content-identical version appears
	}{
		{"empty changes", map[string]any{}, 1, 1, false},
		{"nil changes", nil, 1, 1, false},
		{"all fields rewritten with same values",
			map[string]any{"host": "a", "port": 80, "retries": 3}, 2, 2, true},
		{"subset rewritten with same values",
			map[string]any{"host": "a"}, 2, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			before := m.CurrentVersion()
			beforeContent := mustContent(t, m, before)
			beforeSources := sourcesOf(t, m)

			got, err := m.Update(tc.changes)
			if err != nil {
				t.Fatalf("Update(%v): %v", tc.changes, err)
			}
			if got != tc.wantVersion {
				t.Fatalf("Update returned %d, want %d", got, tc.wantVersion)
			}
			if cur := m.CurrentVersion(); cur != tc.wantCurrent {
				t.Fatalf("current = %d, want %d", cur, tc.wantCurrent)
			}

			// The old version is untouched either way.
			if c := mustContent(t, m, before); !equalMaps(c, beforeContent) {
				t.Fatalf("old version %d content mutated: %v, want %v", before, c, beforeContent)
			}

			if !tc.wantNewCloned {
				if _, err := m.ContentAt(before + 1); err == nil {
					t.Fatalf("version %d should not exist after no-op update", before+1)
				}
				return
			}

			// A brand-new version exists whose content and per-field
			// sources are identical to the previous version.
			newContent := mustContent(t, m, tc.wantCurrent)
			if !equalMaps(newContent, beforeContent) {
				t.Fatalf("new version content %v differs from old %v", newContent, beforeContent)
			}
			newSources := sourcesOf(t, m)
			if !equalMaps(newSources, beforeSources) {
				t.Fatalf("new version sources %v differ from old %v", newSources, beforeSources)
			}
		})
	}
}

// TestRepeatedNoOpUpdates pins that every all-no-op update advances the
// version number by exactly one while field sources never move.
func TestRepeatedNoOpUpdates(t *testing.T) {
	m := newTestManager(t)
	initialSources := sourcesOf(t, m)
	sameValues := map[string]any{"host": "a", "port": 80, "retries": 3}

	prev := m.CurrentVersion()
	for i := 0; i < 3; i++ {
		v := mustUpdate(t, m, sameValues)
		if v != prev+1 {
			t.Fatalf("no-op update %d returned version %d, want %d", i, v, prev+1)
		}
		prev = v
	}
	if cur := m.CurrentVersion(); cur != prev {
		t.Fatalf("current = %d, want %d", cur, prev)
	}
	if got := sourcesOf(t, m); !equalMaps(got, initialSources) {
		t.Fatalf("sources after repeated no-op updates = %v, want %v", got, initialSources)
	}
	// Every intermediate shell version is retained with identical content.
	wantContent := mustContent(t, m, 1)
	for v := int64(2); v <= prev; v++ {
		if c := mustContent(t, m, v); !equalMaps(c, wantContent) {
			t.Fatalf("version %d content %v, want %v", v, c, wantContent)
		}
	}
}

// TestSwitchToCurrentVersion pins that switching to the current version
// itself is not a no-op: it produces a new, larger version number whose
// content and sources are identical to the version it was cloned from.
func TestSwitchToCurrentVersion(t *testing.T) {
	m := newTestManager(t)
	mustUpdate(t, m, map[string]any{"host": "b"}) // v2, current
	cur := m.CurrentVersion()
	beforeContent := mustContent(t, m, cur)
	beforeSources := sourcesOf(t, m)

	nv, err := m.SwitchTo(cur)
	if err != nil {
		t.Fatalf("SwitchTo(%d): %v", cur, err)
	}
	if nv != cur+1 {
		t.Fatalf("SwitchTo(current) returned %d, want %d", nv, cur+1)
	}
	if got := m.CurrentVersion(); got != nv {
		t.Fatalf("current = %d, want %d", got, nv)
	}
	if c := mustContent(t, m, nv); !equalMaps(c, beforeContent) {
		t.Fatalf("switched content %v, want identical to %v", c, beforeContent)
	}
	if got := sourcesOf(t, m); !equalMaps(got, beforeSources) {
		t.Fatalf("switched sources %v, want identical to %v", got, beforeSources)
	}
}

// TestSwitchToSourceProvenance pins that after switching to a historical
// version, reactivated fields keep the source version of whoever
// originally wrote them, not the version number produced by the switch.
func TestSwitchToSourceProvenance(t *testing.T) {
	m := newTestManager(t)
	mustUpdate(t, m, map[string]any{"host": "b"})  // v2 writes host
	mustUpdate(t, m, map[string]any{"port": 8080}) // v3 writes port
	nv, err := m.SwitchTo(1)                       // v4 clones v1
	if err != nil {
		t.Fatalf("SwitchTo(1): %v", err)
	}
	snap := m.Acquire()
	defer snap.Release()
	for _, name := range []string{"host", "port", "retries"} {
		got, err := snap.SourceVersion(name)
		if err != nil {
			t.Fatalf("SourceVersion(%q): %v", name, err)
		}
		if got != 1 {
			t.Fatalf("SourceVersion(%q) = %d, want 1 (original writer), not switch version %d",
				name, got, nv)
		}
	}
}
