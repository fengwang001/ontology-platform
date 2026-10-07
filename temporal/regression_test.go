package temporal

import "testing"

// TestMultipleLinksSameCommit regression-tests that one commit creating
// several links with the same source (or destination) publishes exactly one
// combined adjacency root and preserves all edges.
func TestMultipleLinksSameCommit(t *testing.T) {
	s := NewStore()
	tx := s.Begin()
	tx.CreateObjectType("T", nil)
	tx.CreateLinkType("e", Cardinality{MaxOut: -1})
	mustCommit(t, tx)
	tx = s.Begin()
	tx.CreateObject("a", "T", nil)
	for i := 0; i < 10; i++ {
		tx.CreateObject(ObjectID("n"+itoa(i)), "T", nil)
	}
	mustCommit(t, tx)
	tx = s.Begin()
	for i := 0; i < 10; i++ {
		if err := tx.CreateLink("e", "a", ObjectID("n"+itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	at := mustCommit(t, tx)

	sn, err := s.Snapshot(at)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := sn.LinksFrom("a", "", func(Link) bool { n++; return true }); err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("want 10 links in one commit, got %d", n)
	}

	// Same destination from many sources in one commit (in-bucket merge).
	tx = s.Begin()
	for i := 0; i < 7; i++ {
		if err := tx.CreateLink("e", ObjectID("n"+itoa(i)), "a"); err != nil {
			t.Fatal(err)
		}
	}
	at2 := mustCommit(t, tx)
	sn2, _ := s.Snapshot(at2)
	// Out-degree of a is 10 still; plus incoming links don't affect outgoing.
	if err := sn2.LinksFrom("a", "", func(Link) bool { n++; return true }); err != nil {
		t.Fatal(err)
	}
	if n != 20 {
		t.Fatalf("after incoming links want still 10 outgoing (counted twice=%d), got %d", n, n-10)
	}

	// Deleting an object with many links cascades all of them in one commit.
	tx = s.Begin()
	tx.DeleteObject("a")
	at3 := mustCommit(t, tx)
	sn3, _ := s.Snapshot(at3)
	for i := 0; i < 10; i++ {
		alive, err := sn3.LinkExists("e", "a", ObjectID("n"+itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		if alive {
			t.Fatalf("cascaded link a->n%d must be revoked after delete", i)
		}
	}
	_, err = s.Traverse(TraversalConfig{Start: "a", At: at3,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrStartNotFound {
		t.Fatalf("deleted start must be ErrStartNotFound, got %v", err)
	}
}
