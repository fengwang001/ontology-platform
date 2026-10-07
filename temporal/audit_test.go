package temporal

import "testing"

// TestAuditRecordsDecisions verifies that every input, fixed baseline and
// consulted version/conclusion during a traversal is recorded, so results can
// be reconstructed after the fact.
func TestAuditRecordsDecisions(t *testing.T) {
	s := NewStore()
	tx := s.Begin()
	tx.CreateObjectType("T", []Property{{Name: "p", Type: "int"}})
	tx.CreateLinkType("e", Cardinality{MaxOut: -1})
	mustCommit(t, tx)
	tx = s.Begin()
	tx.CreateObject("a", "T", PropertyValues{"p": 1})
	tx.CreateObject("b", "T", PropertyValues{"p": 2})
	mustCommit(t, tx)
	tx = s.Begin()
	mustOK(t, tx.CreateLink("e", "a", "b"))
	mustCommit(t, tx)

	audit := &MemoryAudit{}
	res, err := s.Traverse(TraversalConfig{
		Start: "a", At: 3,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1},
	}, audit)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Objects) != 2 {
		t.Fatalf("want 2 objects, got %d", len(res.Objects))
	}

	steps := map[string]int{}
	for _, r := range audit.Records {
		if r.At != 3 {
			t.Fatalf("audit record anchored at wrong instant: %+v", r)
		}
		if r.TraversalID == 0 {
			t.Fatalf("audit record missing traversal id")
		}
		steps[r.Step]++
	}

	// Must record: start lookup, every link existence decision, and a type
	// version anchor for every visited object.
	if steps["lookup_start"] != 1 {
		t.Fatalf("want 1 lookup_start record, got %d (%+v)", steps["lookup_start"], steps)
	}
	if steps["link_exists"] < 1 {
		t.Fatalf("want at least 1 link_exists record, got %d", steps["link_exists"])
	}
	if steps["anchor_type_version"] != 2 {
		t.Fatalf("want 2 type-anchor records, got %d (%+v)", steps["anchor_type_version"], steps)
	}

	// The link existence record must carry full decision inputs.
	var found bool
	for _, r := range audit.Records {
		if r.Step == "link_exists" && r.Src == "a" && r.Dst == "b" &&
			r.LinkType == "e" && r.Decision == "exists_at_baseline" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing full link_exists audit record: %+v", audit.Records)
	}

	// Two concurrent traversals get distinct ids but a shared sink keeps
	// records attributable.
	audit2 := &MemoryAudit{}
	if _, err := s.Traverse(TraversalConfig{Start: "a", At: 3,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, audit2); err != nil {
		t.Fatal(err)
	}
	id1 := audit.Records[0].TraversalID
	id2 := audit2.Records[0].TraversalID
	if id1 == id2 {
		t.Fatalf("traversal ids must be unique: %d", id1)
	}
}

// TestAuditBaselineStable verifies audit records always carry the requested
// baseline even when the schema has migrated before the traversal runs.
func TestAuditBaselineStable(t *testing.T) {
	s := NewStore()
	tx := s.Begin()
	tx.CreateObjectType("T", []Property{{Name: "old", Type: "int"}})
	mustCommit(t, tx)
	tx = s.Begin()
	tx.CreateObject("a", "T", PropertyValues{"old": 1})
	mustCommit(t, tx)
	tx = s.Begin()
	tx.MigrateObjectType("T", []Property{{Name: "new", Type: "int"}})
	mustCommit(t, tx)

	audit := &MemoryAudit{}
	if _, err := s.Traverse(TraversalConfig{Start: "a", At: 2,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, audit); err != nil {
		t.Fatal(err)
	}
	var anchoredOld bool
	for _, r := range audit.Records {
		if r.Step == "anchor_type_version" && r.At == 2 && r.Detail == "old" {
			anchoredOld = true
		}
	}
	if !anchoredOld {
		t.Fatalf("audit must show pre-migration property 'old' anchored at 2: %+v", audit.Records)
	}
}
