package ontology

import (
	"testing"
	"time"
)

// corr is a compact expected Correction: a value plus its transaction
// bounds in seconds. txTo == 0 means "still current" (zero TxTo).
type corr struct {
	value        string
	txFrom, txTo int64
}

// checkTrajectory asserts the exact correction trajectory at validAt.
func checkTrajectory(t *testing.T, s *Store, ent, prop string, validAt int64, want []corr) {
	t.Helper()
	got, err := s.Corrections(ent, prop, ts(validAt))
	if err != nil {
		t.Fatalf("Corrections(validAt=%d): %v", validAt, err)
	}
	if len(got) != len(want) {
		t.Fatalf("Corrections(validAt=%d): want %d entries, got %d: %+v",
			validAt, len(want), len(got), got)
	}
	for i, w := range want {
		var wantTxTo time.Time
		if w.txTo != 0 {
			wantTxTo = ts(w.txTo)
		}
		if g := got[i]; g.Value != w.value || !g.TxFrom.Equal(ts(w.txFrom)) || !g.TxTo.Equal(wantTxTo) {
			t.Errorf("Corrections(validAt=%d)[%d] = %+v, want {%q txFrom=%d txTo=%d}",
				validAt, i, g, w.value, w.txFrom, w.txTo)
		}
	}
}

// Baseline: a single write with no overlap yields a one-entry trajectory
// everywhere inside its valid interval, edges included.
func TestCorrectionsBaselineSingleWrite(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "a", ts(10), ts(20), ts(1))

	for _, at := range []int64{10, 15, 19} {
		checkTrajectory(t, s, "e", "p", at, []corr{{"a", 1, 0}})
	}
}

// All three split shapes (left / right / middle overwrite): a validAt
// landing in a residual region gets a two-entry trajectory whose second
// entry carries the SAME value as the first, with TxFrom equal to the
// split (close) time tx=2 — not the original write time tx=1.
func TestCorrectionsAfterSplitShapes(t *testing.T) {
	shapes := []struct {
		name           string
		newFrom, newTo int64
		residualProbes []int64 // validAt points inside residual region(s)
		coveredProbe   int64   // validAt inside the overwritten region
	}{
		{"left-overwrite", 5, 15, []int64{17, 19}, 12},    // residual [15,20)
		{"right-overwrite", 15, 25, []int64{10, 12}, 17},  // residual [10,15)
		{"middle-overwrite", 13, 16, []int64{11, 17}, 14}, // residuals [10,13), [16,20)
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			s := NewStore()
			mustWrite(t, s, "e", "p", "old", ts(10), ts(20), ts(1))
			mustWrite(t, s, "e", "p", "new", ts(sh.newFrom), ts(sh.newTo), ts(2))

			for _, probe := range sh.residualProbes {
				checkTrajectory(t, s, "e", "p", probe, []corr{
					{"old", 1, 2},
					{"old", 2, 0}, // residual: same value, TxFrom = split time
				})
			}
			checkTrajectory(t, s, "e", "p", sh.coveredProbe, []corr{
				{"old", 1, 2},
				{"new", 2, 0},
			})
		})
	}
}

// The trajectory in a residual region contains adjacent entries with
// identical values, differing only in transaction boundaries: the first
// entry's TxTo equals the second entry's TxFrom. No coalescing happens.
func TestCorrectionsAdjacentSameValueEntries(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), ts(20), ts(1))
	mustWrite(t, s, "e", "p", "new", ts(13), ts(16), ts(2))

	got, err := s.Corrections("e", "p", ts(11))
	if err != nil {
		t.Fatalf("Corrections: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	if got[0].Value != got[1].Value {
		t.Errorf("adjacent entries differ in value: %+v", got)
	}
	if !got[0].TxTo.Equal(got[1].TxFrom) {
		t.Errorf("adjacent entries not contiguous in tx time: %+v", got)
	}
}

// Chained splits: repeated overlapping writes keep appending same-value
// entries to the trajectory of a point whose value never changed.
func TestCorrectionsChainedSplitsAccumulate(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "v", ts(10), ts(20), ts(1))
	mustWrite(t, s, "e", "p", "w", ts(15), ts(25), ts(2)) // carves right side
	mustWrite(t, s, "e", "p", "x", ts(5), ts(12), ts(3))  // carves left side

	// validAt=13 held "v" throughout; no correction ever happened there.
	checkTrajectory(t, s, "e", "p", 13, []corr{
		{"v", 1, 2},
		{"v", 2, 3},
		{"v", 3, 0},
	})
}

// Splitting an infinite interval also produces a same-value residual entry
// in the trajectory of points far outside the overwritten region.
func TestCorrectionsResidualOfInfiniteInterval(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "old", ts(10), time.Time{}, ts(1))
	mustWrite(t, s, "e", "p", "new", ts(15), ts(20), ts(2))

	checkTrajectory(t, s, "e", "p", 100, []corr{
		{"old", 1, 2},
		{"old", 2, 0},
	})
}
