package ontology

import (
	"reflect"
	"testing"
)

// snapshotValuesAndSources pins the current version and reads back every
// declared field's value and source version, then releases the snapshot.
func snapshotValuesAndSources(t *testing.T, m *Manager) (map[string]any, map[string]int64) {
	t.Helper()
	snap := m.Acquire()
	defer snap.Release()
	values := make(map[string]any)
	sources := make(map[string]int64)
	for _, d := range testDecls() {
		v, err := snap.Get(d.Name)
		if err != nil {
			t.Fatalf("Get(%q): %v", d.Name, err)
		}
		sv, err := snap.SourceVersion(d.Name)
		if err != nil {
			t.Fatalf("SourceVersion(%q): %v", d.Name, err)
		}
		values[d.Name] = v
		sources[d.Name] = sv
	}
	return values, sources
}

// TestUpdateEmptyVsNoopAdvance pins the asymmetry between an empty Update
// (no version advance) and a non-empty but fully no-op Update (version
// advances, content and sources identical to the previous version).
func TestUpdateEmptyVsNoopAdvance(t *testing.T) {
	cases := []struct {
		name          string
		changes       map[string]any
		wantAdvance   bool
		wantNewSource bool // whether any field source moves to the new version
	}{
		{name: "nil changes", changes: nil, wantAdvance: false},
		{name: "empty changes", changes: map[string]any{}, wantAdvance: false},
		{name: "single no-op field", changes: map[string]any{"host": "a"}, wantAdvance: true},
		{name: "all fields no-op", changes: map[string]any{"host": "a", "port": 80, "retries": 3}, wantAdvance: true},
		{name: "mixed no-op and real change", changes: map[string]any{"host": "a", "port": 8080}, wantAdvance: true, wantNewSource: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			before := m.CurrentVersion()
			beforeValues, beforeSources := snapshotValuesAndSources(t, m)

			got, err := m.Update(tc.changes)
			if err != nil {
				t.Fatalf("Update(%v): %v", tc.changes, err)
			}

			wantVersion := before
			if tc.wantAdvance {
				wantVersion = before + 1
			}
			if got != wantVersion {
				t.Fatalf("Update returned %d, want %d", got, wantVersion)
			}
			if cur := m.CurrentVersion(); cur != wantVersion {
				t.Fatalf("current = %d, want %d", cur, wantVersion)
			}

			afterValues, afterSources := snapshotValuesAndSources(t, m)
			if tc.wantNewSource {
				// Only the really-changed field moves, in value and source.
				if afterValues["port"] != int64(8080) {
					t.Fatalf("port = %v, want 8080", afterValues["port"])
				}
				delete(afterValues, "port")
				delete(beforeValues, "port")
				if afterSources["port"] != wantVersion {
					t.Fatalf("port source = %d, want %d", afterSources["port"], wantVersion)
				}
				delete(afterSources, "port")
				delete(beforeSources, "port")
			}
			if !reflect.DeepEqual(afterValues, beforeValues) {
				t.Fatalf("values changed: before %v, after %v", beforeValues, afterValues)
			}
			if !reflect.DeepEqual(afterSources, beforeSources) {
				t.Fatalf("sources changed: before %v, after %v", beforeSources, afterSources)
			}

			if tc.wantAdvance {
				// The no-op update leaves a shell version whose content and
				// sources are field-by-field identical to its parent.
				newContent, err := m.ContentAt(wantVersion)
				if err != nil {
					t.Fatalf("ContentAt(%d): %v", wantVersion, err)
				}
				oldContent, err := m.ContentAt(before)
				if err != nil {
					t.Fatalf("ContentAt(%d): %v", before, err)
				}
				if !tc.wantNewSource && !reflect.DeepEqual(newContent, oldContent) {
					t.Fatalf("no-op version content %v differs from parent %v", newContent, oldContent)
				}
			} else if _, err := m.ContentAt(before + 1); err == nil {
				t.Fatalf("version %d must not exist after empty update", before+1)
			}
		})
	}
}

// TestRepeatedNoopUpdates pins that every non-empty no-op Update advances
// the version by exactly one while field sources never move.
func TestRepeatedNoopUpdates(t *testing.T) {
	m := newTestManager(t)
	_, beforeSources := snapshotValuesAndSources(t, m)
	forms := []map[string]any{
		{"host": "a"},
		{"host": "a", "port": 80},
		{"host": "a", "port": 80, "retries": 3},
	}
	prev := m.CurrentVersion()
	for i, changes := range forms {
		got, err := m.Update(changes)
		if err != nil {
			t.Fatalf("round %d Update(%v): %v", i, changes, err)
		}
		if got != prev+1 {
			t.Fatalf("round %d: version = %d, want %d", i, got, prev+1)
		}
		if cur := m.CurrentVersion(); cur != got {
			t.Fatalf("round %d: current = %d, want %d", i, cur, got)
		}
		prev = got
	}
	_, afterSources := snapshotValuesAndSources(t, m)
	if !reflect.DeepEqual(afterSources, beforeSources) {
		t.Fatalf("sources moved after no-op updates: before %v, after %v", beforeSources, afterSources)
	}
}

// TestSwitchToSelfProducesIdenticalVersion pins that switching to the
// current version is not a no-op: it clones a new, larger version whose
// content and sources are identical to the current one.
func TestSwitchToSelfProducesIdenticalVersion(t *testing.T) {
	m := newTestManager(t)
	mustUpdate(t, m, map[string]any{"host": "b"})
	cur := m.CurrentVersion()
	beforeValues, beforeSources := snapshotValuesAndSources(t, m)

	nv, err := m.SwitchTo(cur)
	if err != nil {
		t.Fatalf("SwitchTo(%d): %v", cur, err)
	}
	if nv != cur+1 {
		t.Fatalf("SwitchTo(self) = %d, want %d", nv, cur+1)
	}
	if got := m.CurrentVersion(); got != nv {
		t.Fatalf("current = %d, want %d", got, nv)
	}
	afterValues, afterSources := snapshotValuesAndSources(t, m)
	if !reflect.DeepEqual(afterValues, beforeValues) {
		t.Fatalf("values differ after self-switch: before %v, after %v", beforeValues, afterValues)
	}
	if !reflect.DeepEqual(afterSources, beforeSources) {
		t.Fatalf("sources differ after self-switch: before %v, after %v", beforeSources, afterSources)
	}
}

// TestSwitchToHistoricalKeepsOriginalSources pins that after switching to a
// historical version, reactivated fields keep the source version of the
// update that originally wrote them, not the version produced by the switch.
func TestSwitchToHistoricalKeepsOriginalSources(t *testing.T) {
	m := newTestManager(t)
	v2 := mustUpdate(t, m, map[string]any{"host": "b"})
	v3 := mustUpdate(t, m, map[string]any{"port": 8080})
	nv, err := m.SwitchTo(1)
	if err != nil {
		t.Fatalf("SwitchTo(1): %v", err)
	}
	if nv <= v3 {
		t.Fatalf("switch version = %d, want > %d", nv, v3)
	}
	// Content is v1's, and every field's source points back to the
	// original writer (version 1), not to nv.
	values, sources := snapshotValuesAndSources(t, m)
	if values["host"] != "a" || values["port"] != int64(80) || values["retries"] != int64(3) {
		t.Fatalf("switched content = %v, want v1 content", values)
	}
	for name, sv := range sources {
		if sv != 1 {
			t.Fatalf("SourceVersion(%q) = %d, want 1 (original writer, not switch version %d)", name, sv, nv)
		}
	}

	// Switching to v2 reactivates host="b"; its source still points to v2,
	// the version that wrote it, not to the new switch version.
	nv2, err := m.SwitchTo(v2)
	if err != nil {
		t.Fatalf("SwitchTo(%d): %v", v2, err)
	}
	_, sources2 := snapshotValuesAndSources(t, m)
	if sources2["host"] != v2 {
		t.Fatalf("SourceVersion(host) = %d, want %d (original writer, not switch version %d)", sources2["host"], v2, nv2)
	}
	if sources2["port"] != 1 || sources2["retries"] != 1 {
		t.Fatalf("untouched sources = %v, want 1", sources2)
	}
}
