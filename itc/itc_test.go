package itc

import (
	"errors"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustString(t *testing.T, r *Registry, name string) string {
	t.Helper()
	s, err := r.String(name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSpecScenario replays the exact sequence called out in the task and
// asserts each resulting string character for character.
func TestSpecScenario(t *testing.T) {
	r := NewRegistry()
	must(t, r.Seed("a"))
	must(t, r.Event("a"))
	got := mustString(t, r, "a")
	t.Logf(`Seed("a"); Event("a") -> %s (basis: grow(1,0) gives event 1)`, got)
	if got != "(1;1)" {
		t.Fatalf("after seed+event: got %q want %q", got, "(1;1)")
	}

	parent, child, err := r.Fork("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(`Fork("a","b") -> parent=%s child=%s (basis: fork(1)=((1,0),(0,1)), event shared)`, parent, child)
	if parent != "((1,0);1)" || child != "((0,1);1)" {
		t.Fatalf("fork: got %q %q", parent, child)
	}

	must(t, r.Event("a"))
	must(t, r.Event("b"))
	sa := mustString(t, r, "a")
	sb := mustString(t, r, "b")
	t.Logf(`Event("a"); Event("b") -> %s and %s (basis: grow descends the only live side)`, sa, sb)
	if sa != "((1,0);(1,1,0))" || sb != "((0,1);(1,0,1))" {
		t.Fatalf("events: got %q %q", sa, sb)
	}

	joined, err := r.Join("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(`Join("a","b") -> %s (basis: sum((1,0),(0,1))=1; joined event lifts base to 2)`, joined)
	if joined != "(1;2)" {
		t.Fatalf("join: got %q want %q", joined, "(1;2)")
	}
}

// TestJoinIndependentSeedsOverlap checks that joining two independently seeded
// identities is rejected as an identity overlap.
func TestJoinIndependentSeedsOverlap(t *testing.T) {
	r := NewRegistry()
	must(t, r.Seed("a"))
	must(t, r.Seed("b"))
	_, err := r.Join("a", "b")
	t.Logf(`Join("a","b") of two seeds -> %v (basis: sum(1,1) is an overlap)`, err)
	if !errors.Is(err, ErrIdentityOverlap) {
		t.Fatalf("got %v want ErrIdentityOverlap", err)
	}
	if _, err := r.String("a"); err != nil {
		t.Fatalf("rejected join must not mutate state: %v", err)
	}
	if _, err := r.String("b"); err != nil {
		t.Fatalf("rejected join must keep b alive: %v", err)
	}
}

// TestFillBranches exercises each node-level fill rule:
// fill((1,ir),...), fill((il,1),...) and the generic fill((il,ir),...).
func TestFillBranches(t *testing.T) {
	// (1,ir) branch: i=(1,0), e=(0,(0,1,0),0). The 1 leaf demands that the
	// whole event component reachable from this node be raised to its max.
	i1 := idPair(idOne(), idZero())
	e1 := evNode(0, evNode(0, evInt(1), evInt(0)), evInt(0))
	got1 := evFill(i1, e1)
	want1 := evNode(0, evInt(1), evInt(0))
	t.Logf("fill((1,0),(0,(0,1,0),0)) = %s (basis: fill((1,ir),(n,el,er)))", got1)
	if got1.String() != want1.String() {
		t.Fatalf("(1,ir) branch: got %s want %s", got1, want1)
	}

	// (il,1) branch: i=(0,1), e=(0,0,(0,0,1)).
	i2 := idPair(idZero(), idOne())
	e2 := evNode(0, evInt(0), evNode(0, evInt(0), evInt(1)))
	got2 := evFill(i2, e2)
	want2 := evNode(0, evInt(0), evInt(1))
	t.Logf("fill((0,1),(0,0,(0,0,1))) = %s (basis: fill((il,1),(n,el,er)))", got2)
	if got2.String() != want2.String() {
		t.Fatalf("(il,1) branch: got %s want %s", got2, want2)
	}

	// Generic (il,ir) branch with neither outer child equal to 1:
	// i=((1,0),(0,1)), e=(0,(0,(0,1,0),0),0). The generic rule recurses;
	// the nested (1,0) collapses (0,(0,1,0),0) to (0,1,0,0,0).
	i3 := idPair(idPair(idOne(), idZero()), idPair(idZero(), idOne()))
	e3 := evNode(0, evNode(0, evNode(0, evInt(1), evInt(0)), evInt(0)), evInt(0))
	got3 := evFill(i3, e3)
	want3 := "(0,(0,1,0),0)"
	t.Logf("fill(((1,0),(0,1)),(0,(0,(0,1,0),0),0)) = %s (basis: generic fill((il,ir),(n,el,er)))", got3)
	if got3.String() != want3 {
		t.Fatalf("generic branch: got %s want %s", got3, want3)
	}
	if got3.String() == e3.String() {
		t.Fatal("generic branch must change the tree")
	}
}

// TestGrowTiePicksRight verifies that when cl == cr the right subtree grows.
func TestGrowTiePicksRight(t *testing.T) {
	// i=((1,0),(0,1)), e=(0,(0,1,0),(0,0,1)): each side reaches its 1 leaf
	// at cost 1; the tie must grow the right subtree. Note that (0,0,0)
	// normalizes to the integer 0, so asymmetric node children are required.
	i := idPair(idPair(idOne(), idZero()), idPair(idZero(), idOne()))
	el := evNode(0, evInt(1), evInt(0))
	er := evNode(0, evInt(0), evInt(1))
	grown, cost := evGrow(i, evNode(0, el, er))
	want := "(0,(0,1,0),(0,0,2))"
	t.Logf("grow(((1,0),(0,1)),(0,(0,1,0),(0,0,1))) = (%s, %d) (basis: cl==cr==1 picks right)", grown, cost)
	if grown.String() != want {
		t.Fatalf("tie: got %s want %s", grown, want)
	}
	if cost != 2 {
		t.Fatalf("tie cost: got %d want %d (inner cost 1 on each side, outer +1)", cost, 2)
	}
}

// TestGrowIntegerPenalty checks the +1000000 penalty when an integer event
// meets a node identity, and that grow(1,n) has no penalty.
func TestGrowIntegerPenalty(t *testing.T) {
	i := idPair(idOne(), idZero())
	grown, cost := evGrow(i, evInt(3))
	t.Logf("grow((1,0),3) = (%s, %d) (basis: integer expanded to raw (3,0,0), penalty +1000000)", grown, cost)
	if grown.String() != "(3,1,0)" {
		t.Fatalf("penalty grow: got %s", grown)
	}
	if cost != 1000001 {
		t.Fatalf("penalty cost: got %d want %d", cost, 1000001)
	}

	g1, c1 := evGrow(idOne(), evInt(7))
	t.Logf("grow(1,7) = (%s, %d) (basis: grow(1,n)=(n+1,0))", g1, c1)
	if g1.String() != "8" || c1 != 0 {
		t.Fatalf("grow(1,n): got %s %d", g1, c1)
	}
}

// TestCompareRelations covers all four outcomes of Compare.
func TestCompareRelations(t *testing.T) {
	r := NewRegistry()
	must(t, r.Seed("a"))
	_, _, err := r.Fork("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	assertRel(t, r, "a", "b", Equal, "two fresh seeds")

	must(t, r.Event("a"))
	assertRel(t, r, "a", "b", After, "a recorded one event after fork: its event (0,1,0) exceeds 0")
	assertRel(t, r, "b", "a", Before, "mirror of the previous comparison")

	must(t, r.Event("b"))
	assertRel(t, r, "a", "b", Concurrent, "both recorded independent events after fork")
}

func assertRel(t *testing.T, r *Registry, a, b string, want Relation, basis string) {
	t.Helper()
	got, err := r.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(`Compare(%q,%q) = %s (basis: %s)`, a, b, got, basis)
	if got != want {
		t.Fatalf("compare %s vs %s: got %s want %s", a, b, got, want)
	}
}
