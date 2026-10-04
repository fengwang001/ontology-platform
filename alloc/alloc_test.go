package alloc

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/decider"
	"ontology/node"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newCluster(t *testing.T, l, h int) *node.Cluster {
	t.Helper()
	cl, err := node.NewCluster(l, h)
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func addNodes(t *testing.T, cl *node.Cluster, spec [][2]string, total int64) {
	t.Helper()
	for _, n := range spec {
		must(t, cl.AddNode(n[0], n[1], total))
	}
}

func movementsString(ms []Movement) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		role := "P"
		if !m.Primary {
			role = "R"
		}
		if m.From == "" {
			parts[i] = fmt.Sprintf("%s/%d/%s->%s", m.Index, m.Shard, role, m.To)
		} else {
			parts[i] = fmt.Sprintf("%s/%d/%s:%s->%s", m.Index, m.Shard, role, m.From, m.To)
		}
	}
	return strings.Join(parts, ",")
}

func assertMovements(t *testing.T, label string, got []Movement, want string) {
	t.Helper()
	if s := movementsString(got); s != want {
		t.Fatalf("%s = [%s], want [%s]", label, s, want)
	}
}

func TestExamplesFromSpec(t *testing.T) {
	t.Run("example1", func(t *testing.T) {
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z1"}, {"n3", "z2"}}, 100)
		must(t, cl.CreateIndex("x", 1, 2, 30))
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "x/0/P->n1,x/0/R->n2,x/0/R->n3")
		assertMovements(t, "moved", res.Moved, "")
	})

	t.Run("example2", func(t *testing.T) {
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z1"}, {"n3", "z2"}}, 100)
		must(t, cl.SetOther("n3", 55))
		must(t, cl.CreateIndex("y", 1, 1, 30))
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "y/0/P->n1")
		vs, err := Explain(cl, "y", 0, false)
		must(t, err)
		wantR := []decider.Reason{decider.D2SameShard, decider.D3Awareness, decider.D4Disk}
		for i, v := range vs {
			if v.Reason != wantR[i] {
				t.Fatalf("node %s = %s, want %s", v.NodeID, v.Reason, wantR[i])
			}
		}
		must(t, cl.SetOther("n3", 50))
		res = Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "y/0/R->n3")
		must(t, cl.SetOther("n3", 65))
		res = Reroute(cl)
		assertMovements(t, "moved", res.Moved, "")
		if id, ok := cl.Placement(node.CopyKey{Index: "y", Shard: 0, Replica: 0}); !ok || id != "n3" {
			t.Fatalf("replica must remain on n3, got %q/%v", id, ok)
		}
	})
}

func TestWatermarkAsymmetryAndEquality(t *testing.T) {
	t.Run("primary may reach H but not exceed; replica bound L", func(t *testing.T) {
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}}, 100)
		must(t, cl.SetOther("n1", 61)) // 61+30=91 > 90 primary veto
		must(t, cl.SetOther("n2", 51)) // 51+30=81 <= 90 primary ok; replica would fail
		must(t, cl.CreateIndex("a", 1, 0, 30))
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "a/0/P->n2")
	})

	t.Run("equality at both watermarks passes", func(t *testing.T) {
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}}, 100)
		must(t, cl.SetOther("n1", 60)) // 60+30 = 90 == H
		must(t, cl.SetOther("n2", 50)) // 50+30 = 80 == L
		must(t, cl.CreateIndex("e", 1, 1, 30))
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "e/0/P->n1,e/0/R->n2")
	})

	t.Run("migration always uses low watermark", func(t *testing.T) {
		// Primary on excluded n1; n2 sits at 51: migration D4 uses L=80 -> veto,
		// even though 81 would satisfy H.
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}}, 100)
		must(t, cl.CreateIndex("p", 1, 0, 30))
		_ = Reroute(cl)
		must(t, cl.SetOther("n2", 51))
		must(t, cl.SetExclude("n1", true))
		res := Reroute(cl)
		assertMovements(t, "moved", res.Moved, "")
	})
}

func TestAwareness(t *testing.T) {
	t.Run("ceil 5 over 2 is 3", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z1"}, {"n3", "z1"}, {"n4", "z2"}, {"n5", "z2"}}, 1000)
		must(t, cl.CreateIndex("big", 1, 4, 1))
		_ = Reroute(cl)
		if z1, z2 := cl.ZoneCopies("z1", "big", 0), cl.ZoneCopies("z2", "big", 0); z1 != 3 || z2 != 2 {
			t.Fatalf("zone counts = %d/%d, want 3/2", z1, z2)
		}
	})

	t.Run("zone count change changes cap", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z1"}}, 1000)
		must(t, cl.CreateIndex("ix", 1, 1, 1))
		_ = Reroute(cl)
		if cl.ZoneCopies("z1", "ix", 0) != 2 {
			t.Fatal("with one zone both copies fit there")
		}
		must(t, cl.AddNode("n3", "z2", 1000))
		must(t, cl.CreateIndex("ix2", 1, 1, 1))
		_ = Reroute(cl)
		if z1, z2 := cl.ZoneCopies("z1", "ix2", 0), cl.ZoneCopies("z2", "ix2", 0); z1 != 1 || z2 != 1 {
			t.Fatalf("new index must spread: %d/%d", z1, z2)
		}
	})

	t.Run("excluded nodes still counted in z", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}, {"n3", "z3"}}, 1000)
		must(t, cl.SetExclude("n3", true))
		must(t, cl.CreateIndex("q", 1, 1, 1)) // c=2,z=3,ceil=1: only one copy per zone
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "q/0/P->n1,q/0/R->n2")
	})
}

func TestOrderingAndSkip(t *testing.T) {
	t.Run("same-round replica follows just-placed primary", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}}, 1000)
		must(t, cl.CreateIndex("f", 1, 1, 1))
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "f/0/P->n1,f/0/R->n2")
	})

	t.Run("primary missing skips replicas and Explain flags it", func(t *testing.T) {
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}}, 100)
		must(t, cl.SetExclude("n1", true))
		must(t, cl.CreateIndex("g", 1, 2, 10))
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "")
		vs, err := Explain(cl, "g", 0, false)
		must(t, err)
		for _, v := range vs {
			if !v.PrimaryUp {
				t.Fatalf("%s verdict must carry PrimaryUp", v.NodeID)
			}
		}
		vsP, err := Explain(cl, "g", 0, true)
		must(t, err)
		for _, v := range vsP {
			if v.PrimaryUp {
				t.Fatal("primary verdict must not carry PrimaryUp")
			}
		}
	})

	t.Run("index/shard/primary-replica processing order", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}, {"n3", "z3"}, {"n4", "z4"},
			{"n5", "z5"}, {"n6", "z6"}}, 1000)
		must(t, cl.CreateIndex("b", 2, 0, 1))
		must(t, cl.CreateIndex("a", 2, 0, 1))
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned,
			"a/0/P->n1,a/1/P->n2,b/0/P->n3,b/1/P->n4")
	})

	t.Run("tie broken by smallest id byte order", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n10", "z1"}, {"n2", "z2"}}, 1000)
		must(t, cl.CreateIndex("t", 1, 0, 1))
		res := Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "t/0/P->n10")
	})

	t.Run("fewest copies wins regardless of id", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}, {"n3", "z3"}}, 1000)
		must(t, cl.CreateIndex("load", 1, 0, 1))
		_ = Reroute(cl) // primary -> n1
		must(t, cl.CreateIndex("more", 2, 0, 1))
		res := Reroute(cl)
		// n2 and n3 have 0 copies; n1 has 1. Both new primaries avoid n1.
		assertMovements(t, "assigned", res.Assigned, "more/0/P->n2,more/1/P->n3")
	})
}

func TestMigration(t *testing.T) {
	t.Run("excluded node moves all copies off", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		// n2 shares n1's zone; with the moving copy removed from the D3 count
		// (ceil(3/3)=1), it is the zone's free slot.
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z1"}, {"n3", "z2"}, {"n4", "z3"}}, 1000)
		must(t, cl.CreateIndex("e", 1, 2, 10))
		_ = Reroute(cl)
		must(t, cl.SetExclude("n1", true))
		res := Reroute(cl)
		if len(res.Moved) != 1 || res.Moved[0].From != "n1" || res.Moved[0].To != "n2" {
			t.Fatalf("want exactly one move off n1, got %s", movementsString(res.Moved))
		}
		if cl.CopyCount("n1") != 0 {
			t.Fatal("excluded node must end empty")
		}
		// Excluded node never receives copies later.
		res = Reroute(cl)
		assertMovements(t, "assigned", res.Assigned, "")
	})

	t.Run("move stays when no target exists", func(t *testing.T) {
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}}, 1000)
		must(t, cl.CreateIndex("m", 1, 1, 10))
		_ = Reroute(cl)
		must(t, cl.SetExclude("n1", true))
		res := Reroute(cl)
		assertMovements(t, "moved", res.Moved, "")
		if cl.CopyCount("n1") != 1 {
			t.Fatal("copy must remain in place when move impossible")
		}
	})

	t.Run("over-high node stops once usage not greater than H", func(t *testing.T) {
		// n1 total 100 with two 30-byte copies: other 65 -> 125 > 90, but
		// placement round rejects it (95 > 90) so set the pressure afterwards:
		// 65 + 30 = 95 > H; moving one copy drops n1 to 65 <= H: exactly one.
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}, {"n3", "z3"}}, 100)
		must(t, cl.CreateIndex("h", 2, 0, 30))
		_ = Reroute(cl)
		must(t, cl.SetOther("n1", 65))
		before := cl.CopyCount("n1")
		res := Reroute(cl)
		if len(res.Moved) != 1 {
			t.Fatalf("want exactly 1 move (stop at boundary), got %s", movementsString(res.Moved))
		}
		if cl.CopyCount("n1") != before-1 {
			t.Fatalf("n1 copies = %d, want %d", cl.CopyCount("n1"), before-1)
		}
		if used := cl.Used("n1"); used*100 > 90*100 {
			t.Fatalf("n1 still over H: used=%d", used)
		}
	})

	t.Run("migration processes largest copies first", func(t *testing.T) {
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}, {"n3", "z3"}, {"n4", "z4"}}, 1000)
		// Put a small and a large copy on n1 by assigning indices in an order
		// the allocator naturally lands on n1; instead force via reroute with
		// n1 the first node.
		must(t, cl.CreateIndex("small", 1, 0, 10))
		_ = Reroute(cl) // small/0 -> n1
		must(t, cl.CreateIndex("large", 1, 0, 100))
		_ = Reroute(cl) // large/0 -> n2 (n1 has 1 copy)
		// Rearrange deterministically: move large onto n1 manually.
		cl.Lock()
		cl.RemoveLocked("n2", node.CopyKey{Index: "large", Shard: 0, Primary: true, Size: 100})
		cl.PutLocked("n1", node.CopyKey{Index: "large", Shard: 0, Primary: true, Size: 100})
		cl.Unlock()
		must(t, cl.SetExclude("n1", true))
		res := Reroute(cl)
		if len(res.Moved) < 1 || res.Moved[0].Index != "large" {
			t.Fatalf("largest copy must migrate first, got %s", movementsString(res.Moved))
		}
	})

	t.Run("D3 counts with source copy removed first", func(t *testing.T) {
		// c=2,z=2,cap=1. P n1(z1), R n3(z2); n4(z2) empty. Exclude n3: after
		// the conceptual removal z2 has 0 copies, so n4 passes D3. Without the
		// decrement n4 would see z2=1 and be vetoed.
		cl := newCluster(t, 80, 99)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z1"}, {"n3", "z2"}, {"n4", "z2"}}, 1000)
		must(t, cl.CreateIndex("d", 1, 1, 10)) // c=2,z=2,cap1
		_ = Reroute(cl)                        // P n1(z1), R n3(z2)
		must(t, cl.SetExclude("n3", true))
		res := Reroute(cl)
		assertMovements(t, "moved", res.Moved, "d/0/R:n3->n4")
	})

	t.Run("space freed in phase 2 is not reused until next round", func(t *testing.T) {
		cl := newCluster(t, 80, 90)
		addNodes(t, cl, [][2]string{{"n1", "z1"}, {"n2", "z2"}}, 100)
		must(t, cl.SetOther("n2", 70))
		must(t, cl.CreateIndex("d", 1, 1, 30))
		res := Reroute(cl)
		assertMovements(t, "round1 assigned", res.Assigned, "d/0/P->n1")
		must(t, cl.SetOther("n1", 65)) // n1 95 > H
		must(t, cl.SetOther("n2", 20))
		res = Reroute(cl)
		// Phase 1 first: replica -> n2. Then primary can only target n2 (D2).
		// Nothing can fill freed space on n1 within this round.
		assertMovements(t, "round2 assigned", res.Assigned, "d/0/R->n2")
		assertMovements(t, "round2 moved", res.Moved, "")
	})
}

func TestExplainErrorsAndReadonly(t *testing.T) {
	cl := newCluster(t, 80, 90)
	addNodes(t, cl, [][2]string{{"n1", "z1"}}, 100)
	must(t, cl.CreateIndex("i", 2, 0, 10))
	if _, err := Explain(cl, "missing", 0, true); !errors.Is(err, node.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if _, err := Explain(cl, "i", 2, true); !errors.Is(err, node.ErrShardMissing) {
		t.Fatalf("want shard missing, got %v", err)
	}
	if _, err := Explain(cl, "i", -1, true); !errors.Is(err, node.ErrShardMissing) {
		t.Fatalf("want shard missing for negative, got %v", err)
	}
	vs, err := Explain(cl, "i", 0, true)
	must(t, err)
	if len(vs) != 1 || vs[0].Reason != decider.OK {
		t.Fatalf("explain = %+v", vs)
	}
	// Explain must not assign anything.
	if _, ok := cl.Placement(node.CopyKey{Index: "i", Shard: 0, Primary: true}); ok {
		t.Fatal("Explain mutated state")
	}
}
