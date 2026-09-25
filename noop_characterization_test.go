package ontology

import (
	"reflect"
	"testing"
)

// stateAtLocked reads the stored content + provenance of a version without
// going through a pinned snapshot. Characterization tests need to inspect
// historical (possibly current) versions side by side.
func stateAt(t *testing.T, m *Manager, version int64) (map[string]any, map[string]int64) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	vs, ok := m.versions[version]
	if !ok {
		t.Fatalf("version %d not stored", version)
	}
	values := make(map[string]any, len(vs.values))
	for k, v := range vs.values {
		values[k] = v
	}
	sources := make(map[string]int64, len(vs.sources))
	for k, v := range vs.sources {
		sources[k] = v
	}
	return values, sources
}

func sameState(t *testing.T, m *Manager, a, b int64) {
	t.Helper()
	va, sa := stateAt(t, m, a)
	vb, sb := stateAt(t, m, b)
	if !reflect.DeepEqual(va, vb) {
		t.Fatalf("values differ: v%d = %#v, v%d = %#v", a, va, b, vb)
	}
	if !reflect.DeepEqual(sa, sb) {
		t.Fatalf("sources differ: v%d = %#v, v%d = %#v", a, sa, b, sb)
	}
}

// TestUpdateEmptyVsAllNoOp characterizes the asymmetry between an empty
// Update and a non-empty Update whose values all equal the current ones.
// Current (observed) behavior: empty changes are an in-place no-op, any
// non-empty changes map advances the version number even when every value
// is identical and no field source moves.
func TestUpdateEmptyVsAllNoOp(t *testing.T) {
	cases := []struct {
		name    string
		changes map[string]any
		repeat  int
		advance bool
	}{
		{name: "nil", changes: nil, repeat: 3, advance: false},
		{name: "empty", changes: map[string]any{}, repeat: 3, advance: false},
		{name: "one field same value", changes: map[string]any{"host": "a"}, repeat: 3, advance: true},
		{name: "two fields same values", changes: map[string]any{"host": "a", "port": 80}, repeat: 3, advance: true},
		{name: "all fields same values", changes: map[string]any{"host": "a", "port": 80, "retries": 3}, repeat: 3, advance: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			start := m.CurrentVersion()
			prev := start
			_, baseSources := stateAt(t, m, start)
			for i := 0; i < tc.repeat; i++ {
				got, err := m.Update(tc.changes)
				if err != nil {
					t.Fatalf("Update #%d: %v", i+1, err)
				}
				if tc.advance {
					if got != prev+1 {
						t.Fatalf("Update #%d returned %d, want %d (version advances by 1)", i+1, got, prev+1)
					}
					if m.CurrentVersion() != got {
						t.Fatalf("current = %d, want %d", m.CurrentVersion(), got)
					}
					// Empty-shell version: number moved but content and
					// per-field provenance are field-for-field identical.
					sameState(t, m, prev, got)
					_, gotSources := stateAt(t, m, got)
					if !reflect.DeepEqual(gotSources, baseSources) {
						t.Fatalf("sources moved on no-op #%d: %#v, want %#v", i+1, gotSources, baseSources)
					}
				} else {
					if got != start {
						t.Fatalf("Update #%d returned %d, want %d (empty changes stays put)", i+1, got, start)
					}
					if m.CurrentVersion() != start {
						t.Fatalf("current = %d, want %d", m.CurrentVersion(), start)
					}
				}
				prev = got
			}
		})
	}
}

// TestSwitchToCurrentIsNotNoOp characterizes SwitchTo(target) when target
// is already the current version. Current (observed) behavior: it still
// allocates the next version number and stores a field-for-field clone,
// including the provenance map.
func TestSwitchToCurrentIsNotNoOp(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, m *Manager) int64
		repeat int
	}{
		{
			name:   "current is version 1",
			setup:  func(t *testing.T, m *Manager) int64 { return m.CurrentVersion() },
			repeat: 3,
		},
		{
			name: "current after a real update",
			setup: func(t *testing.T, m *Manager) int64 {
				mustUpdate(t, m, map[string]any{"host": "b"})
				return m.CurrentVersion()
			},
			repeat: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			target := tc.setup(t, m)
			prev := m.CurrentVersion()
			for i := 0; i < tc.repeat; i++ {
				nv, err := m.SwitchTo(target)
				if err != nil {
					t.Fatalf("SwitchTo #%d: %v", i+1, err)
				}
				if nv != prev+1 {
					t.Fatalf("SwitchTo(current) #%d = %d, want %d", i+1, nv, prev+1)
				}
				if m.CurrentVersion() != nv {
					t.Fatalf("current = %d, want %d", m.CurrentVersion(), nv)
				}
				sameState(t, m, prev, nv)
				prev = nv
				target = nv // every iteration switches to the current self
			}
		})
	}
}

// TestSwitchToProvenancePointsToOriginalWriter characterizes which
// version a reactivated field claims as its source after SwitchTo.
// Current (observed) behavior: SwitchTo clones the source version's
// sources map, so SourceVersion keeps pointing at the version that
// originally wrote the value, not at the switch version.
func TestSwitchToProvenancePointsToOriginalWriter(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, m *Manager) int64 // returns the switch result version
		sources map[string]int64                     // expected sources at the result
	}{
		{
			name: "switch back to version 1",
			setup: func(t *testing.T, m *Manager) int64 {
				mustUpdate(t, m, map[string]any{"host": "b"}) // v2
				nv, err := m.SwitchTo(1)
				if err != nil {
					t.Fatal(err)
				}
				return nv
			},
			sources: map[string]int64{"host": 1, "port": 1, "retries": 1},
		},
		{
			name: "older per-field writers all survive a switch",
			setup: func(t *testing.T, m *Manager) int64 {
				mustUpdate(t, m, map[string]any{"host": "b"})  // v2: host written by 2
				mustUpdate(t, m, map[string]any{"port": 8080}) // v3: port written by 3
				nv, err := m.SwitchTo(1)                       // v4 restores initial content
				if err != nil {
					t.Fatal(err)
				}
				return nv
			},
			sources: map[string]int64{"host": 1, "port": 1, "retries": 1},
		},
		{
			name: "provenance survives switching to a switch result",
			setup: func(t *testing.T, m *Manager) int64 {
				mustUpdate(t, m, map[string]any{"host": "b"}) // v2
				s1, err := m.SwitchTo(1)                      // v3: clone of v1
				if err != nil {
					t.Fatal(err)
				}
				s2, err := m.SwitchTo(s1) // v4: clone of v3
				if err != nil {
					t.Fatal(err)
				}
				return s2
			},
			sources: map[string]int64{"host": 1, "port": 1, "retries": 1},
		},
		{
			name: "post-switch update advances only the written field",
			setup: func(t *testing.T, m *Manager) int64 {
				mustUpdate(t, m, map[string]any{"host": "b"}) // v2
				if _, err := m.SwitchTo(1); err != nil {      // v3 restores host=a
					t.Fatal(err)
				}
				return mustUpdate(t, m, map[string]any{"retries": 7}) // v4
			},
			sources: map[string]int64{"host": 1, "port": 1, "retries": 4},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			nv := tc.setup(t, m)
			if m.CurrentVersion() != nv {
				t.Fatalf("current = %d, want %d", m.CurrentVersion(), nv)
			}
			_, got := stateAt(t, m, nv)
			if !reflect.DeepEqual(got, tc.sources) {
				t.Fatalf("sources at v%d = %#v, want %#v", nv, got, tc.sources)
			}
		})
	}
}
