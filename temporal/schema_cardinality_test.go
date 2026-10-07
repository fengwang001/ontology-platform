package temporal

import "testing"

// TestPropertyMigrationBoundary enumerates the boundary cases around an
// object-type property migration that lands strictly after the traversal
// instant but before the traversal physically runs.
func TestPropertyMigrationBoundary(t *testing.T) {
	s := NewStore()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	}

	tx := s.Begin()
	tx.CreateObjectType("Person", []Property{
		{Name: "name", Type: "string"},
		{Name: "age", Type: "int"},
	})
	tx.CreateLinkType("knows", Cardinality{MaxOut: -1})
	mustCommit(t, tx)

	tx = s.Begin()
	tx.CreateObject("a", "Person", PropertyValues{"name": "Alice", "age": 30})
	mustCommit(t, tx)

	tx = s.Begin()
	tx.CreateObject("b", "Person", PropertyValues{"name": "Bob", "age": 41})
	must(tx.CreateLink("knows", "a", "b"))
	mustCommit(t, tx)

	// Baseline instant used for all traversals: 3.
	baseline := Instant(3)

	// Migration AFTER baseline (instant 4): drop "age", rename-add "score".
	tx = s.Begin()
	tx.MigrateObjectType("Person", []Property{
		{Name: "name", Type: "string"},
		{Name: "score", Type: "int"},
	})
	mustCommit(t, tx)

	// Traversal physically happens at head=4 but must interpret properties
	// under the definition covering instant 3 (name+age).
	audit := &MemoryAudit{}
	res, err := s.Traverse(TraversalConfig{
		Start: "a", At: baseline,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1},
	}, audit)
	must(err)

	if len(res.Objects) != 2 {
		t.Fatalf("want 2 visited, got %d", len(res.Objects))
	}
	for _, o := range res.Objects {
		if _, ok := o.State.Properties["age"]; !ok {
			t.Fatalf("object %s must keep pre-migration property age, got %v",
				o.State.ID, o.State.Properties)
		}
		if _, ok := o.State.Properties["score"]; ok {
			t.Fatalf("object %s must not expose post-migration property score", o.State.ID)
		}
	}

	// The audit trail must show the type version anchored at the baseline.
	foundAnchor := false
	for _, r := range audit.Records {
		if r.Step == "anchor_type_version" && r.At == baseline {
			foundAnchor = true
			if r.Decision != "property_definition_anchored_at_baseline" || r.Detail != "name,age" {
				t.Fatalf("anchor audit = %+v", r)
			}
		}
	}
	if !foundAnchor {
		t.Fatalf("missing type-version anchor audit record")
	}

	// Direct snapshot at 3 also sees the OLD definition; snapshot at 4 sees
	// the new one — proving the version switch is boundary-exact.
	sn3, _ := s.Snapshot(3)
	props3, ok, err := sn3.ObjectTypeProps("Person")
	must(err)
	if !ok || propertyNames(props3) != "name,age" {
		t.Fatalf("props@3 = %v", props3)
	}
	sn4, _ := s.Snapshot(4)
	props4, ok, err := sn4.ObjectTypeProps("Person")
	must(err)
	if !ok || propertyNames(props4) != "name,score" {
		t.Fatalf("props@4 = %v", props4)
	}

	// Repeating the same traversal after yet another migration must still
	// produce identical output (no "migration completed or not" fork).
	tx = s.Begin()
	tx.MigrateObjectType("Person", []Property{{Name: "onlyName", Type: "string"}})
	mustCommit(t, tx)
	res2, err := s.Traverse(TraversalConfig{
		Start: "a", At: baseline,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1},
	}, nil)
	must(err)
	if !resultsEqual(res, res2) {
		t.Fatalf("repeated baseline traversal changed after a later migration")
	}
}

// TestCardinalityExactVersion checks that traversal uses the real link set at
// the baseline and the cardinality version that *covers* it exactly — never
// the current rule and never the nearest of a long version sequence.
func TestCardinalityExactVersion(t *testing.T) {
	s := NewStore()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	}

	tx := s.Begin()
	tx.CreateObjectType("N", []Property{{Name: "v", Type: "int"}})
	// Version 1 of cardinality: unbounded.
	tx.CreateLinkType("edge", Cardinality{MaxOut: -1})
	mustCommit(t, tx)

	tx = s.Begin()
	for _, id := range []ObjectID{"s", "a", "b", "c"} {
		tx.CreateObject(id, "N", PropertyValues{"v": 1})
	}
	mustCommit(t, tx)

	// At instants 3,4,5 create three outgoing links while unbounded.
	for _, dst := range []ObjectID{"a", "b", "c"} {
		tx = s.Begin()
		must(tx.CreateLink("edge", "s", dst))
		mustCommit(t, tx)
	}
	baseline := Instant(5)

	// Build a long sequence of cardinality adjustments AFTER the baseline so
	// a naive "nearest version" strategy could easily pick the wrong one.
	cards := []int{2, 3, 1, 4, 2, 5, 1, 3, 0, 2}
	for i, maxOut := range cards {
		tx = s.Begin()
		tx.AdjustCardinality("edge", Cardinality{MaxOut: maxOut})
		mustCommit(t, tx)
		_ = i
	}

	// Current cardinality is tightened to MaxOut=1: two of the three real
	// links are "not allowed now", yet the baseline traversal must follow all
	// three links that truly existed at 5.
	res, err := s.Traverse(TraversalConfig{
		Start: "s", At: baseline,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1},
	}, nil)
	must(err)
	if len(res.Links) != 3 {
		t.Fatalf("baseline traversal must follow the 3 links that existed at %d, got %d: %+v",
			baseline, len(res.Links), res.Links)
	}
	if len(res.Objects) != 4 {
		t.Fatalf("want 4 objects, got %d", len(res.Objects))
	}

	// The cardinality version consulted exactly at baseline is unbounded.
	sn, _ := s.Snapshot(baseline)
	card, ok, err := sn.Cardinality("edge")
	must(err)
	if !ok || card.MaxOut != -1 {
		t.Fatalf("cardinality@%d = %+v,%v want unbounded", baseline, card, ok)
	}

	// Each intermediate adjustment is independently addressable: version at
	// the first adjustment instant (6) is MaxOut=2, and at the last (15) is
	// MaxOut=2 as well; version 12 (zero based: third card) is 1.
	checks := []struct {
		at     Instant
		maxOut int
	}{{6, 2}, {7, 3}, {8, 1}, {15, 2}}
	for _, c := range checks {
		sn, _ := s.Snapshot(c.at)
		got, ok, err := sn.Cardinality("edge")
		must(err)
		if !ok || got.MaxOut != c.maxOut {
			t.Fatalf("cardinality@%d = %d (ok=%v), want %d", c.at, got.MaxOut, ok, c.maxOut)
		}
	}

	// A new link under the tightened CURRENT rule must be rejected at commit
	// time (current out-degree 3 > MaxOut), proving the tightening affects
	// only future writes, never the historical link set.
	tx = s.Begin()
	if err := tx.CreateLink("edge", "s", "x_missing_type_ok"); err == nil {
		// object x missing so precheck catches it; create object first.
		tx.Rollback()
	} else {
		tx.Rollback()
	}
	tx = s.Begin()
	tx.CreateObject("d", "N", PropertyValues{"v": 1})
	mustCommit(t, tx)
	tx = s.Begin()
	err = tx.CreateLink("edge", "s", "d")
	if err != nil {
		t.Fatalf("staging error unexpected: %v", err)
	}
	if _, err := tx.Commit(); err == nil {
		t.Fatalf("commit must reject the 4th link under current MaxOut=2")
	}
}

func resultsEqual(a, b *TraversalResult) bool {
	if a.At != b.At || len(a.Objects) != len(b.Objects) || len(a.Links) != len(b.Links) {
		return false
	}
	for i := range a.Objects {
		x, y := a.Objects[i], b.Objects[i]
		if x.State.ID != y.State.ID || x.Depth != y.Depth || x.State.Type != y.State.Type {
			return false
		}
		if len(x.State.Properties) != len(y.State.Properties) {
			return false
		}
		for k, v := range x.State.Properties {
			if y.State.Properties[k] != v {
				return false
			}
		}
	}
	for i := range a.Links {
		if a.Links[i] != b.Links[i] {
			return false
		}
	}
	return true
}
