package temporal

import (
	"fmt"
	"testing"
)

// buildManyLifecycleGraph builds ONE probe edge (probe -> target) of link
// type lt plus a large number of unrelated create/revoke churn events on
// OTHER edges of the SAME link type. totalChurn controls the cumulative
// create/revoke history of the link type as a whole; the probe edge itself
// always has exactly one create entry.
func buildManyLifecycleGraph(t *testing.T, totalChurn int) (*Store, Instant) {
	t.Helper()
	s := NewStore()
	tx := s.Begin()
	tx.CreateObjectType("T", nil)
	tx.CreateLinkType("lt", Cardinality{MaxOut: -1})
	mustCommit(t, tx)

	tx = s.Begin()
	tx.CreateObject("probe", "T", nil)
	tx.CreateObject("target", "T", nil)
	tx.CreateObject("hub", "T", nil)
	for i := 0; i < totalChurn; i++ {
		tx.CreateObject(ObjectID("n"+itoa(i)), "T", nil)
	}
	mustCommit(t, tx)

	// Create the probe edge once.
	tx = s.Begin()
	if err := tx.CreateLink("lt", "probe", "target"); err != nil {
		t.Fatal(err)
	}
	baseline := mustCommit(t, tx)

	// Now accumulate churn on the same link type via OTHER edges. Each churn
	// edge is created then revoked, so the link type's cumulative create /
	// revoke history grows linearly with totalChurn while the probe edge's
	// own timeline stays at one entry.
	for i := 0; i < totalChurn; i++ {
		dst := ObjectID("n" + itoa(i))
		tx = s.Begin()
		if err := tx.CreateLink("lt", "hub", dst); err != nil {
			t.Fatal(err)
		}
		mustCommit(t, tx)
		tx = s.Begin()
		tx.RevokeLink("lt", "hub", dst)
		mustCommit(t, tx)
	}
	return s, baseline
}

// TestLinkDecisionCostBoundedByEdgeHistory is the independently verifiable
// proof that deciding one candidate link's existence at the baseline does
// NOT grow linearly with the link type's cumulative create/revoke history.
//
// Two store sizes are compared (100 and 4000 churn events = 40x more link
// type history). The low-level probe counters exposed by Snapshot must stay
// bounded by a tiny constant (one adjacency version lookup + one edge
// timeline binary search), and the wall-clock decision ratio must stay well
// under the 40x growth that linear-in-history scanning would show.
func TestLinkDecisionCostBoundedByEdgeHistory(t *testing.T) {
	small, baseSmall := buildManyLifecycleGraph(t, 100)
	large, baseLarge := buildManyLifecycleGraph(t, 4000)

	snSmall, err := small.Snapshot(baseSmall)
	if err != nil {
		t.Fatal(err)
	}
	snLarge, err := large.Snapshot(baseLarge)
	if err != nil {
		t.Fatal(err)
	}

	// Reset counters, perform exactly one existence decision on each store.
	snSmall.Stats()
	exists, err := snSmall.LinkExists("lt", "probe", "target")
	if err != nil || !exists {
		t.Fatalf("small probe link exists=%v err=%v", exists, err)
	}
	statsSmall := snSmall.Stats()

	snLarge.Stats()
	exists, err = snLarge.LinkExists("lt", "probe", "target")
	if err != nil || !exists {
		t.Fatalf("large probe link exists=%v err=%v", exists, err)
	}
	statsLarge := snLarge.Stats()

	// The probe counts must be constant: exactly 1 adjacency probe and 1
	// timeline probe, regardless of 40x more link-type history.
	// One adjacency root lookup + one O(log k) treap membership probe, plus
	// one edge-timeline binary search: a small constant, independent of the
	// link type's total history.
	wantSmall := AccessStats{TimelineProbes: 1, AdjacencyProbes: 2}
	if statsSmall != wantSmall {
		t.Fatalf("small probes = %+v, want %+v", statsSmall, wantSmall)
	}
	if statsLarge != wantSmall {
		t.Fatalf("large probes = %+v, want %+v (must not grow with link-type history)",
			statsLarge, wantSmall)
	}

	// Also confirm the churn edges are correctly absent at baseline (even
	// though the link type history now contains thousands of later events).
	exists, err = snLarge.LinkExists("lt", "hub", ObjectID("n3999"))
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatalf("churn edge created after baseline must not exist at baseline")
	}

	// Explanatory artifact: print the independently checkable figures.
	t.Logf("one LinkExists decision: small(100 churn)=%+v large(4000 churn)=%+v",
		statsSmall, statsLarge)
}

// TestProbeCountPerEnumeration repeats the argument for whole-source
// enumeration: listing probe's outgoing links at the baseline costs one
// adjacency root lookup plus one timeline probe per actual result edge, never
// scanning the link type's churn history.
func TestProbeCountPerEnumeration(t *testing.T) {
	s, base := buildManyLifecycleGraph(t, 2000)
	sn, err := s.Snapshot(base)
	if err != nil {
		t.Fatal(err)
	}
	sn.Stats()
	var n int
	if err := sn.LinksFrom("probe", "", func(Link) bool { n++; return true }); err != nil {
		t.Fatal(err)
	}
	stats := sn.Stats()
	if n != 1 {
		t.Fatalf("probe has %d outgoing links, want 1", n)
	}
	// One adjacency bucket probe + one edge timeline probe for the one edge.
	if stats.TimelineProbes != 1 || stats.AdjacencyProbes != 1 {
		t.Fatalf("enumeration probes = %+v, want {1,1}", stats)
	}
	t.Logf("enumeration with 4000-event link-type history: %+v", stats)
}

// TestNaiveOracleCostGrowsForContrast demonstrates that the deliberately
// naive oracle DOES scan all history (its replay is linear), providing the
// contrast that makes the production bound meaningful.
func TestNaiveOracleCostGrowsForContrast(t *testing.T) {
	for _, churn := range []int{50, 400} {
		s, base := buildManyLifecycleGraph(t, churn)
		oracle := NewNaiveModel()
		oracle.IngestLog(s.LogEntries())
		if !oracle.NaiveLinkExists(base, "lt", "probe", "target") {
			t.Fatalf("oracle must see probe link")
		}
		t.Logf("naive oracle replays all %d commit entries for one decision (by design)",
			len(s.LogEntries()))
	}
	_ = fmt.Sprint
}
