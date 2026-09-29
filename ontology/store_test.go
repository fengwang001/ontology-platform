package ontology

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func vecGE(a, b []int64) bool {
	for i := range a {
		if a[i] < b[i] {
			return false
		}
	}
	return true
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestStore(t *testing.T, replicas int) (*Store, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	s, err := New(replicas, buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, buf
}

func tailLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// [0,2,0] has a larger component 1 but is lexicographically older than
// [1,0,0], so it is ignored; [2,0,0] is newer and overwrites.
func TestLexicographicOverwrite(t *testing.T) {
	s, _ := newTestStore(t, 3)
	must(t, s.Put("k", 0, []int64{1, 0, 0}, "a"))
	if got := s.View().Entries["k"].Value; got != "a" {
		t.Fatalf("initial value = %q, want a", got)
	}

	must(t, s.Put("k", 1, []int64{0, 2, 0}, "stale"))
	if got := s.View().Entries["k"]; got.Value != "a" || got.Tomb {
		t.Fatalf("older-by-lex event applied: %+v", got)
	}

	must(t, s.Put("k", 0, []int64{2, 0, 0}, "b"))
	if got := s.View().Entries["k"].Value; got != "b" {
		t.Fatalf("newer value = %q, want b", got)
	}

	must(t, s.Put("k", 1, []int64{2, 0, 0}, "dup"))
	if got := s.View().Entries["k"].Value; got != "b" {
		t.Fatalf("duplicate event changed value: %q", got)
	}
	t.Logf("lexicographic overwrite result: %+v", s.View().Entries["k"])
}

func TestStableVectorMonotonic(t *testing.T) {
	s, _ := newTestStore(t, 3)
	steps := []Event{
		{Key: "a", Replica: 0, Vector: []int64{3, 1, 0}, Value: "1"},
		{Key: "a", Replica: 1, Vector: []int64{3, 2, 0}, Value: "2"},
		{Key: "a", Replica: 2, Vector: []int64{1, 2, 5}, Value: "3"},
		{Key: "a", Replica: 0, Vector: []int64{3, 2, 5}, Value: "4"},
		{Key: "a", Replica: 1, Vector: []int64{3, 2, 5}, Value: "5"},
		{Key: "a", Replica: 2, Vector: []int64{3, 2, 5}, Value: "6"},
	}
	want := [][]int64{
		{0, 0, 0},
		{0, 0, 0},
		{1, 1, 0},
		{1, 2, 0},
		{1, 2, 5},
		{3, 2, 5},
	}
	prev := []int64{0, 0, 0}
	for i, e := range steps {
		must(t, s.Apply([]Event{e}))
		got := s.StableVector()
		if !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("step %d stable = %v, want %v", i, got, want[i])
		}
		if !vecGE(got, prev) {
			t.Fatalf("stable vector regressed: %v -> %v", prev, got)
		}
		t.Logf("step %d input=%v stable=%v (>= previous %v)", i, e.Vector, got, prev)
		prev = got
	}
}

func TestReclaimOnlyStableTombstones(t *testing.T) {
	s, _ := newTestStore(t, 3)
	must(t, s.Put("live", 0, []int64{5, 0, 0}, "keep"))
	must(t, s.Delete("tomb-dominated", 0, []int64{6, 0, 0}))
	must(t, s.Delete("tomb-past-frontier", 0, []int64{6, 2, 0}))

	must(t, s.Apply([]Event{
		{Key: "tick1", Replica: 1, Vector: []int64{6, 1, 0}, Value: "x"},
		{Key: "tick2", Replica: 2, Vector: []int64{6, 0, 1}, Value: "y"},
	}))
	if got := s.StableVector(); !reflect.DeepEqual(got, []int64{6, 0, 0}) {
		t.Fatalf("stable = %v, want [6,0,0]", got)
	}

	removed := s.Reclaim()
	if !reflect.DeepEqual(removed, []string{"tomb-dominated"}) {
		t.Fatalf("removed = %v, want [tomb-dominated]", removed)
	}
	view := s.View().Entries
	if _, ok := view["tomb-dominated"]; ok {
		t.Fatal("dominated tombstone not removed")
	}
	if _, ok := view["tomb-past-frontier"]; !ok {
		t.Fatal("tombstone past stability frontier was wrongly reclaimed")
	}
	if e := view["live"]; e.Tomb || e.Value != "keep" {
		t.Fatalf("live entry disturbed: %+v", e)
	}

	must(t, s.Apply([]Event{
		{Key: "tick3", Replica: 0, Vector: []int64{6, 2, 0}, Value: "x"},
		{Key: "tick4", Replica: 1, Vector: []int64{6, 2, 0}, Value: "x"},
		{Key: "tick5", Replica: 2, Vector: []int64{6, 2, 1}, Value: "y"},
	}))
	removed = s.Reclaim()
	if !reflect.DeepEqual(removed, []string{"tomb-past-frontier"}) {
		t.Fatalf("second removed = %v", removed)
	}
	if got := s.ReapedKeys(); !reflect.DeepEqual(got, []string{"tomb-dominated", "tomb-past-frontier"}) {
		t.Fatalf("reaped history = %v", got)
	}
	t.Logf("reclaim result: %v, remaining entries=%d", s.ReapedKeys(), len(s.View().Entries))
}

// Late/duplicate writes after a delete must not resurrect the key, while the
// stale events still advance their origin replica clocks.
func TestOldEventDoesNotResurrect(t *testing.T) {
	s, _ := newTestStore(t, 3)
	must(t, s.Put("k", 0, []int64{1, 0, 0}, "v1"))
	must(t, s.Delete("k", 0, []int64{2, 0, 0}))

	must(t, s.Put("k", 1, []int64{1, 5, 0}, "zombie"))
	must(t, s.Put("k", 2, []int64{2, 0, 0}, "zombie2"))
	if e := s.View().Entries["k"]; !e.Tomb {
		t.Fatalf("deleted key resurrected: %+v", e)
	}

	must(t, s.Apply([]Event{
		{Key: "tick", Replica: 0, Vector: []int64{2, 5, 0}, Value: "t"},
		{Key: "tick", Replica: 1, Vector: []int64{2, 5, 0}, Value: "t"},
		{Key: "tick", Replica: 2, Vector: []int64{2, 5, 0}, Value: "t"},
	}))
	if got := s.StableVector(); !reflect.DeepEqual(got, []int64{2, 5, 0}) {
		t.Fatalf("stable = %v, want [2,5,0] (stale clocks must count)", got)
	}

	removed := s.Reclaim()
	if !reflect.DeepEqual(removed, []string{"k"}) {
		t.Fatalf("removed = %v, want [k]", removed)
	}
	if _, ok := s.View().Entries["k"]; ok {
		t.Fatal("tombstone not reclaimed once stable")
	}
	must(t, s.Put("k", 1, []int64{1, 9, 9}, "late"))
	if _, ok := s.View().Entries["k"]; ok {
		t.Fatal("late stale event re-created a reclaimed key")
	}
	t.Log("old event ignored after reclamation, key stays gone")
}

func TestInvalidInputsRejectedAtomically(t *testing.T) {
	if _, err := New(0, nil); !errors.Is(err, ErrInvalidReplicas) {
		t.Fatalf("New(0) err = %v, want ErrInvalidReplicas", err)
	}
	if _, err := New(-2, nil); !errors.Is(err, ErrInvalidReplicas) {
		t.Fatalf("New(-2) err = %v, want ErrInvalidReplicas", err)
	}

	s, _ := newTestStore(t, 3)
	good := Event{Key: "ok", Replica: 0, Vector: []int64{1, 0, 0}, Value: "v"}
	cases := []struct {
		name string
		bad  Event
		want error
	}{
		{"replica too large", Event{Key: "x", Replica: 3, Vector: []int64{0, 0, 0}}, ErrReplicaOutOfRange},
		{"replica negative", Event{Key: "x", Replica: -1, Vector: []int64{0, 0, 0}}, ErrReplicaOutOfRange},
		{"negative component", Event{Key: "x", Replica: 0, Vector: []int64{0, -1, 0}}, ErrNegativeVector},
		{"empty key", Event{Key: "", Replica: 0, Vector: []int64{0, 0, 0}}, ErrEmptyKey},
		{"vector too short", Event{Key: "x", Replica: 0, Vector: []int64{0, 0}}, ErrVectorLength},
		{"vector too long", Event{Key: "x", Replica: 0, Vector: []int64{0, 0, 0, 0}}, ErrVectorLength},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Apply([]Event{good, tc.bad})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Apply err = %v, want %v", err, tc.want)
			}
			snap := s.View()
			if len(snap.Entries) != 0 {
				t.Fatalf("entries changed after rejected batch: %v", snap.Entries)
			}
			for r := 0; r < 3; r++ {
				if !reflect.DeepEqual(snap.Clocks[r], []int64{0, 0, 0}) {
					t.Fatalf("clock %d changed after rejected batch: %v", r, snap.Clocks[r])
				}
			}
			if !reflect.DeepEqual(snap.Stable, []int64{0, 0, 0}) {
				t.Fatalf("stable changed after rejected batch: %v", snap.Stable)
			}
			if got := s.ReapedKeys(); len(got) != 0 {
				t.Fatalf("reaped list changed: %v", got)
			}
			t.Logf("rejected %+v with %v; state unchanged", tc.bad, err)
		})
	}
}

func TestConcurrentReadOnlyViewsIdentical(t *testing.T) {
	buf := &bytes.Buffer{}
	s, err := New(3, buf)
	if err != nil {
		t.Fatal(err)
	}
	must(t, s.Apply([]Event{
		{Key: "k1", Replica: 0, Vector: []int64{4, 1, 0}, Value: "a"},
		{Key: "k2", Replica: 1, Vector: []int64{4, 2, 0}, Value: "b"},
		{Key: "gone", Replica: 2, Vector: []int64{1, 2, 3}, Delete: true},
		{Key: "k1", Replica: 1, Vector: []int64{4, 2, 3}, Value: "c"},
		{Key: "k2", Replica: 2, Vector: []int64{4, 2, 3}, Value: "d"},
		{Key: "tick", Replica: 0, Vector: []int64{4, 2, 3}, Value: "e"},
	}))
	s.Reclaim()
	// A stale duplicate must be ignored and logged.
	if err := s.Put("k1", 2, []int64{0, 9, 9}, "old"); err != nil {
		t.Fatal(err)
	}

	const readers = 16
	const iterations = 50
	snaps := make([]Snapshot, readers)
	stables := make([][]int64, readers)
	reaped := make([][]string, readers)
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				snaps[r] = s.View()
				stables[r] = s.StableVector()
				reaped[r] = s.ReapedKeys()
				if err := s.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
				}
			}
		}(r)
	}
	wg.Wait()
	for r := 1; r < readers; r++ {
		if !reflect.DeepEqual(snaps[0], snaps[r]) {
			t.Fatalf("reader %d view differs:\n%+v\nvs\n%+v", r, snaps[0], snaps[r])
		}
		if !reflect.DeepEqual(stables[0], stables[r]) {
			t.Fatalf("reader %d stable differs: %v vs %v", r, stables[0], stables[r])
		}
		if !reflect.DeepEqual(reaped[0], reaped[r]) {
			t.Fatalf("reader %d reaped differs: %v vs %v", r, reaped[0], reaped[r])
		}
	}

	logText := buf.String()
	for _, want := range []string{"apply committed", "event ignored", "reclaim complete", "self-check ok"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q; got:\n%s", want, logText)
		}
	}
	t.Logf("all %d concurrent readers saw identical views; log sample:\n%s", readers, tailLines(logText, 12))
}
