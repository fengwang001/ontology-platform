package temporal

import "testing"

func TestSmokeSnapshotBasics(t *testing.T) {
	s := NewStore()

	tx := s.Begin()
	tx.CreateObjectType("Person", []Property{{Name: "name", Type: "string"}})
	tx.CreateLinkType("knows", Cardinality{MaxOut: -1})
	t1, err := tx.Commit()
	mustOK(t, err)

	tx = s.Begin()
	tx.CreateObject("a", "Person", PropertyValues{"name": "Alice"})
	tx.CreateObject("b", "Person", PropertyValues{"name": "Bob"})
	t2, err := tx.Commit()
	mustOK(t, err)

	tx = s.Begin()
	mustOK(t, tx.CreateLink("knows", "a", "b"))
	t3, err := tx.Commit()
	mustOK(t, err)

	if t1 != 1 || t2 != 2 || t3 != 3 {
		t.Fatalf("instants = %d,%d,%d, want 1,2,3", t1, t2, t3)
	}

	sn, err := s.Snapshot(2)
	mustOK(t, err)
	exists, err := sn.LinkExists("knows", "a", "b")
	mustOK(t, err)
	if exists {
		t.Fatalf("link must not exist at instant 2")
	}

	sn3, err := s.Snapshot(3)
	mustOK(t, err)
	exists, err = sn3.LinkExists("knows", "a", "b")
	mustOK(t, err)
	if !exists {
		t.Fatalf("link must exist at instant 3")
	}

	a, err := sn3.Object("a")
	mustOK(t, err)
	if !a.Exists || a.Properties["name"] != "Alice" {
		t.Fatalf("unexpected object state: %+v", a)
	}

	got, err := s.Traverse(TraversalConfig{
		Start: "a", At: 3,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1},
	}, nil)
	mustOK(t, err)
	if len(got.Objects) != 2 || len(got.Links) != 1 {
		t.Fatalf("traversal = %+v", got)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
