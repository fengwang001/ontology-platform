package reconcile

import (
	"fmt"
	"reflect"
	"testing"

	"ontology/batch"
	"ontology/store"
)

func setup(t *testing.T, n int, present []int, extra []string) (*batch.Batch, *store.Store) {
	t.Helper()
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%03d", i)
	}
	m, err := batch.New("b", keys)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New()
	for _, i := range present {
		if _, err := st.Put(m.Keys[i], batch.RecordValue(m.ID, m.Keys[i])); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range extra {
		if _, err := st.Put(k, batch.RecordValue(m.ID, k)); err != nil {
			t.Fatal(err)
		}
	}
	return m, st
}

func span(lo, hi int) []int {
	var out []int
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}

func TestReport(t *testing.T) {
	cases := []struct {
		name          string
		n             int
		present       []int
		extra         []string
		committed     bool
		wantWritten   [][2]int
		wantGaps      [][2]int
		wantInFlight  int
		wantCommitted int
	}{
		{"full committed", 5, span(0, 5), nil, true, [][2]int{{0, 5}}, nil, 0, 5},
		{"in-flight with gaps", 10, append(span(0, 4), span(6, 8)...), nil, false,
			[][2]int{{0, 4}, {6, 8}}, [][2]int{{4, 6}, {8, 10}}, 6, 0},
		{"extra records", 3, span(0, 3), []string{"ghost"}, false,
			[][2]int{{0, 3}}, nil, 3, 0},
		{"empty store", 4, nil, nil, false, nil, [][2]int{{0, 4}}, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, st := setup(t, c.n, c.present, c.extra)
			rep := Run(m, st, c.committed)
			if !reflect.DeepEqual(rep.Written, c.wantWritten) {
				t.Fatalf("written=%v, want %v", rep.Written, c.wantWritten)
			}
			if !reflect.DeepEqual(rep.Gaps, c.wantGaps) {
				t.Fatalf("gaps=%v, want %v", rep.Gaps, c.wantGaps)
			}
			if !reflect.DeepEqual(rep.Extra, c.extra) {
				t.Fatalf("extra=%v, want %v", rep.Extra, c.extra)
			}
			if rep.InFlightCount != c.wantInFlight || rep.CommittedCount != c.wantCommitted {
				t.Fatalf("counts=(%d,%d), want (%d,%d)",
					rep.InFlightCount, rep.CommittedCount, c.wantInFlight, c.wantCommitted)
			}
		})
	}
}

func TestLinearAccess(t *testing.T) {
	const n = 5000
	m, st := setup(t, n, span(0, n), nil)
	st.ResetAccesses()
	Run(m, st, true)
	if got := st.Accesses(); got > 3*n {
		t.Fatalf("store accesses=%d > 3n=%d", got, 3*n)
	}
}
