package ontology

import (
	"fmt"
	"testing"
)

// TestSelectionCostIndependentOfHistory proves reviewer selection examines a
// number of reviewer records equal to the total reviewer count, regardless of
// how many sheets exist or how many tasks have already completed. The engine
// exposes the exact scan counter for the most recent selection.
func TestSelectionCostIndependentOfHistory(t *testing.T) {
	build := func(numReviewers, numSheets int) (*Engine, []int) {
		e := New()
		for _, g := range []string{"g1", "g2", "g3"} {
			must(t, e.AddGroup(Group{ID: g}))
		}
		must(t, e.AddQuestion(Question{ID: "q", MaxScore: 100, Step: 1, Threshold: 5}))
		for i := range numReviewers {
			g := []string{"g1", "g2", "g3"}[i%3]
			addRev(t, e, revName(i), g, numSheets+10)
		}
		// Create and finalize many earlier sheets to grow history.
		for i := range numSheets {
			sid := sheetName(i)
			must(t, e.AddSheet(Sheet{ID: sid, Question: "q", Student: "oldstu" + sheetName(i)}))
			v := e.Snapshot()
			for _, sh := range v.Sheets {
				if sh.ID != sid {
					continue
				}
				for _, tk := range sh.Tasks {
					if tk.ReviewerID != "" && !tk.Withdrawn {
						must(t, e.Submit(tk.ID, 50))
					}
				}
			}
		}
		return e, nil
	}

	const reviewers = 30
	small, _ := build(reviewers, 5)
	large, _ := build(reviewers, 500)

	// Force one fresh selection in each engine and read the scan count.
	measure := func(e *Engine) int {
		before := e.chooseScans
		must(t, e.AddSheet(Sheet{ID: "probe", Question: "q", Student: "probe-student"}))
		if e.chooseScans <= before {
			// the probe's first selection already updated the counter
		}
		return e.chooseScans
	}

	scansSmall := measure(small)
	scansLarge := measure(large)
	if scansSmall != reviewers {
		t.Fatalf("small history scans = %d, want exactly reviewer count %d", scansSmall, reviewers)
	}
	if scansLarge != reviewers {
		t.Fatalf("large history scans = %d, want exactly reviewer count %d (history-independent)", scansLarge, reviewers)
	}
	if scansSmall != scansLarge {
		t.Fatalf("selection scan count changed with history size: %d vs %d", scansSmall, scansLarge)
	}
}

func revName(i int) string   { return "r" + pad(i) }
func sheetName(i int) string { return "s" + pad(i) }
func pad(i int) string       { return fmt.Sprintf("%03d", i) }
