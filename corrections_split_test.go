package ontology

import (
	"testing"
	"time"
)

// splitWrite is one Write call in a characterization scenario. A vt of 0
// means an infinite valid-to.
type splitWrite struct {
	value  string
	vf, vt int64
	tx     int64
}

func runCorrections(t *testing.T, writes []splitWrite, validAt int64) []Correction {
	t.Helper()
	s := NewStore()
	for _, w := range writes {
		vt := time.Time{}
		if w.vt != 0 {
			vt = ts(w.vt)
		}
		mustWrite(t, s, "e", "p", w.value, ts(w.vf), vt, ts(w.tx))
	}
	got, err := s.Corrections("e", "p", ts(validAt))
	if err != nil {
		t.Fatalf("Corrections(validAt=%d): %v", validAt, err)
	}
	return got
}

func assertCorrections(t *testing.T, got, want []Correction) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("want %d corrections, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("correction %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestCorrectionsAfterSplit pins down the exact correction trajectory
// produced by each split shape. All assertions describe the behavior of
// the current implementation, including same-value "phantom" entries that
// appear in residual regions.
func TestCorrectionsAfterSplit(t *testing.T) {
	cases := []struct {
		name    string
		writes  []splitWrite
		validAt int64
		want    []Correction
	}{
		{
			name:    "baseline: single write, no overlap",
			writes:  []splitWrite{{"a", 10, 20, 1}},
			validAt: 15,
			want:    []Correction{{Value: "a", TxFrom: ts(1)}},
		},
		{
			name:    "baseline: disjoint second write does not split",
			writes:  []splitWrite{{"a", 10, 20, 1}, {"b", 30, 40, 2}},
			validAt: 15,
			want:    []Correction{{Value: "a", TxFrom: ts(1)}},
		},
		{
			name: "left cover: right residual region gains a same-value entry",
			writes: []splitWrite{
				{"old", 10, 20, 1},
				{"new", 5, 15, 2},
			},
			validAt: 17,
			want: []Correction{
				{Value: "old", TxFrom: ts(1), TxTo: ts(2)},
				{Value: "old", TxFrom: ts(2)},
			},
		},
		{
			name: "right cover: left residual region gains a same-value entry",
			writes: []splitWrite{
				{"old", 10, 20, 1},
				{"new", 15, 25, 2},
			},
			validAt: 12,
			want: []Correction{
				{Value: "old", TxFrom: ts(1), TxTo: ts(2)},
				{Value: "old", TxFrom: ts(2)},
			},
		},
		{
			name: "middle split: left residual region gains a same-value entry",
			writes: []splitWrite{
				{"old", 10, 20, 1},
				{"new", 13, 16, 2},
			},
			validAt: 11,
			want: []Correction{
				{Value: "old", TxFrom: ts(1), TxTo: ts(2)},
				{Value: "old", TxFrom: ts(2)},
			},
		},
		{
			name: "middle split: right residual region gains a same-value entry",
			writes: []splitWrite{
				{"old", 10, 20, 1},
				{"new", 13, 16, 2},
			},
			validAt: 18,
			want: []Correction{
				{Value: "old", TxFrom: ts(1), TxTo: ts(2)},
				{Value: "old", TxFrom: ts(2)},
			},
		},
		{
			name: "middle split: overwritten region is a genuine correction",
			writes: []splitWrite{
				{"old", 10, 20, 1},
				{"new", 13, 16, 2},
			},
			validAt: 14,
			want: []Correction{
				{Value: "old", TxFrom: ts(1), TxTo: ts(2)},
				{Value: "new", TxFrom: ts(2)},
			},
		},
		{
			name: "chained splits stack same-value entries",
			writes: []splitWrite{
				{"old", 10, 20, 1},
				{"n1", 13, 16, 2},
				{"n2", 11, 14, 3},
			},
			validAt: 10,
			want: []Correction{
				{Value: "old", TxFrom: ts(1), TxTo: ts(2)},
				{Value: "old", TxFrom: ts(2), TxTo: ts(3)},
				{Value: "old", TxFrom: ts(3)},
			},
		},
		{
			name: "infinite interval carved: right residual region",
			writes: []splitWrite{
				{"old", 10, 0, 1},
				{"new", 15, 20, 2},
			},
			validAt: 25,
			want: []Correction{
				{Value: "old", TxFrom: ts(1), TxTo: ts(2)},
				{Value: "old", TxFrom: ts(2)},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runCorrections(t, tc.writes, tc.validAt)
			assertCorrections(t, got, tc.want)
		})
	}
}

// TestResidualTxFromIsSplitTime pins down that a residual fact's TxFrom is
// the moment the original fact was closed (the splitting write's txAt),
// not the moment the value was originally written.
func TestResidualTxFromIsSplitTime(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), ts(20), ts(1))
	mustWrite(t, s, "e", "p", "new", ts(13), ts(16), ts(2))

	facts := s.entity("e").props["p"]
	for _, r := range []struct {
		name string
		vf   int64
		vt   int64
	}{
		{"left residual", 10, 13},
		{"right residual", 16, 20},
	} {
		f := findFact(t, facts, ts(r.vf), ts(r.vt))
		if f == nil {
			t.Fatalf("%s [%d,%d) missing: %+v", r.name, r.vf, r.vt, facts)
		}
		if !f.TxFrom.Equal(ts(2)) {
			t.Errorf("%s TxFrom = %v, want split time %v", r.name, f.TxFrom, ts(2))
		}
		if f.TxFrom.Equal(ts(1)) {
			t.Errorf("%s TxFrom must not be the original write time %v", r.name, ts(1))
		}
	}
}

// TestCorrectionsPhantomAdjacentEntries pins down that a split makes the
// trajectory in a residual region contain two adjacent entries with the
// same value, differing only in their transaction-time boundaries: the
// first entry's TxTo equals the second entry's TxFrom.
func TestCorrectionsPhantomAdjacentEntries(t *testing.T) {
	got := runCorrections(t, []splitWrite{
		{"old", 10, 20, 1},
		{"new", 13, 16, 2},
	}, 11)

	if len(got) != 2 {
		t.Fatalf("want 2 corrections, got %d: %+v", len(got), got)
	}
	if got[0].Value != got[1].Value {
		t.Errorf("phantom pair must carry identical values: %+v", got)
	}
	if !got[0].TxTo.Equal(got[1].TxFrom) {
		t.Errorf("phantom entries must be adjacent in tx time: %+v", got)
	}
	for i := 1; i < len(got); i++ {
		if !got[i].TxFrom.After(got[i-1].TxFrom) {
			t.Errorf("trajectory not strictly ordered by tx time at %d", i)
		}
	}
}
