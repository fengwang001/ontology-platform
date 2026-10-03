package resolver

import "testing"

func con(name string, args ...Type) Type {
	typ, err := Con(name, args...)
	if err != nil {
		panic(err)
	}
	return typ
}

func vr(i int) Type {
	typ, err := Var(i)
	if err != nil {
		panic(err)
	}
	return typ
}

func listN(inner Type, n int) Type {
	for i := 0; i < n; i++ {
		inner = con("List", inner)
	}
	return inner
}

func mustResolver(t *testing.T, d int) *Resolver {
	t.Helper()
	r, err := NewResolver(d)
	if err != nil {
		t.Fatalf("NewResolver(%d): %v", d, err)
	}
	return r
}

func mustAdd(t *testing.T, r *Resolver, trait string, head Type, ctx []Constraint) int {
	t.Helper()
	id, err := r.AddInstance(trait, head, ctx)
	if err != nil {
		t.Fatalf("AddInstance(%s, %s): %v", trait, head, err)
	}
	return id
}

func mustResolve(t *testing.T, r *Resolver, trait string, typ Type) Result {
	t.Helper()
	res, err := r.Resolve(trait, typ)
	if err != nil {
		t.Fatalf("Resolve(%s, %s): %v", trait, typ, err)
	}
	return res
}

func assertSuccess(t *testing.T, res Result, instance, height int) *Tree {
	t.Helper()
	if res.Category != Success {
		t.Fatalf("expected success, got %v (%v)", res.Category, res.Failure)
	}
	if res.Tree.Instance != instance || res.Tree.Height != height {
		t.Fatalf("expected instance %d height %d, got instance %d height %d",
			instance, height, res.Tree.Instance, res.Tree.Height)
	}
	return res.Tree
}

func assertFailure(t *testing.T, res Result, cat Category, trait, typeText string) {
	t.Helper()
	if res.Category != cat {
		t.Fatalf("expected %v, got %v (%v)", cat, res.Category, res.Failure)
	}
	if res.Failure.Trait != trait || res.Failure.TypeText != typeText {
		t.Fatalf("expected failing goal %s<%s>, got %s<%s>",
			trait, typeText, res.Failure.Trait, res.Failure.TypeText)
	}
}

func assertReject(t *testing.T, err error, reason RejectReason) {
	t.Helper()
	re, ok := err.(*RejectError)
	if !ok {
		t.Fatalf("expected RejectError, got %v", err)
	}
	if re.Reason != reason {
		t.Fatalf("expected rejection %v, got %v (%v)", reason, re.Reason, re)
	}
}

func cached(r *Resolver, trait string, typ Type) bool {
	_, ok := r.cache[goalKey{trait: trait, text: keyText(typ)}]
	return ok
}

func cacheSize(r *Resolver) int { return len(r.cache) }

// The worked example from the specification.
func TestShowExample(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", con("Int"), nil)                                   // 1
	mustAdd(t, r, "Show", con("List", vr(0)), []Constraint{{"Show", vr(0)}}) // 2
	mustAdd(t, r, "Show", con("List", con("Char")), nil)                     // 3

	res := mustResolve(t, r, "Show", con("List", con("Char")))
	assertSuccess(t, res, 3, 1)

	res = mustResolve(t, r, "Show", con("List", con("Int")))
	tree := assertSuccess(t, res, 2, 2)
	if len(tree.Children) != 1 || tree.Children[0].Instance != 1 {
		t.Fatalf("expected child instance 1, got %+v", tree.Children)
	}

	res = mustResolve(t, r, "Show", listN(con("Char"), 2))
	assertSuccess(t, res, 2, 2)

	// Full cache hit: no head match attempts.
	before := r.headMatches
	res = mustResolve(t, r, "Show", listN(con("Char"), 2))
	assertSuccess(t, res, 2, 2)
	if delta := r.headMatches - before; delta != 0 {
		t.Fatalf("cache hit attempted %d head matches, want 0", delta)
	}

	// Instance 4 invalidates only Show(List(List(Char))).
	mustAdd(t, r, "Show", listN(vr(0), 2), []Constraint{{"Show", vr(0)}}) // 4
	if cached(r, "Show", listN(con("Char"), 2)) {
		t.Fatal("Show(List(List(Char))) should have been invalidated")
	}
	for _, typ := range []Type{con("Int"), con("List", con("Char")), con("List", con("Int"))} {
		if !cached(r, "Show", typ) {
			t.Fatalf("Show(%s) should have been kept", typ)
		}
	}

	// Instance 4 is selected; its context fails; no fallback to instance 2.
	res = mustResolve(t, r, "Show", listN(con("Char"), 2))
	assertFailure(t, res, NoInstance, "Show", "Char")
}

func TestNonLinearHead(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Eq", con("Pair", vr(0), vr(0)), nil)

	res := mustResolve(t, r, "Eq", con("Pair", con("Int"), con("Char")))
	assertFailure(t, res, NoInstance, "Eq", "Pair<Int,Char>")

	res = mustResolve(t, r, "Eq", con("Pair", con("Int"), con("Int")))
	assertSuccess(t, res, 1, 1)
}

func TestMoreSpecializedRelation(t *testing.T) {
	inst := func(id int, head Type) instance { return instance{id: id, trait: "T", head: head} }
	listA := inst(1, con("List", vr(0)))
	listChar := inst(2, con("List", con("Char")))
	listListA := inst(3, con("List", con("List", vr(0))))
	pairIntA := inst(4, con("Pair", con("Int"), vr(0)))
	pairAInt := inst(5, con("Pair", vr(0), con("Int")))
	pairIntInt := inst(6, con("Pair", con("Int"), con("Int")))
	pairAA := inst(7, con("Pair", vr(0), vr(0)))
	anyVar := inst(8, vr(0))

	cases := []struct {
		a, b instance
		want bool
	}{
		{listChar, listA, true},   // List<Char> more specialized than List<a>
		{listA, listChar, false},  // asymmetric
		{listListA, listA, true},  // List<List<a>> more specialized than List<a>
		{listA, listListA, false}, // asymmetric
		{pairIntA, pairAInt, false},
		{pairAInt, pairIntA, false}, // incomparable
		{pairIntInt, pairIntA, true},
		{pairIntInt, pairAInt, true},
		{pairAA, pairIntA, false},
		{pairIntA, pairAA, false}, // non-linear head incomparable
		{listA, listA, true},      // reflexive
		{listA, anyVar, true},     // anything is more specialized than a bare variable
		{anyVar, listA, false},
	}
	for _, c := range cases {
		if got := moreSpec(c.a, c.b); got != c.want {
			t.Errorf("moreSpec(%s, %s) = %v, want %v", c.a.head, c.b.head, got, c.want)
		}
	}
}

func TestAmbiguityAndResolution(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Eq", con("Pair", con("Int"), vr(0)), nil)
	mustAdd(t, r, "Eq", con("Pair", vr(0), con("Int")), nil)

	res := mustResolve(t, r, "Eq", con("Pair", con("Int"), con("Int")))
	assertFailure(t, res, Ambiguous, "Eq", "Pair<Int,Int>")

	mustAdd(t, r, "Eq", con("Pair", con("Int"), con("Int")), nil)
	res = mustResolve(t, r, "Eq", con("Pair", con("Int"), con("Int")))
	assertSuccess(t, res, 3, 1)

	// The non-linear head is incomparable with both linear heads.
	r2 := mustResolver(t, 8)
	mustAdd(t, r2, "Eq", con("Pair", con("Int"), vr(0)), nil)
	mustAdd(t, r2, "Eq", con("Pair", vr(0), vr(0)), nil)
	res = mustResolve(t, r2, "Eq", con("Pair", con("Int"), con("Int")))
	assertFailure(t, res, Ambiguous, "Eq", "Pair<Int,Int>")
}

func TestContextOrderDeterminesFailure(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "F", con("Int"), nil)
	mustAdd(t, r, "G", con("Pair", vr(0), vr(1)),
		[]Constraint{{"F", vr(0)}, {"F", vr(1)}})
	mustAdd(t, r, "H", con("Pair", vr(0), vr(1)),
		[]Constraint{{"F", vr(1)}, {"F", vr(0)}})

	res := mustResolve(t, r, "G", con("Pair", con("Char"), con("Bool")))
	assertFailure(t, res, NoInstance, "F", "Char")

	res = mustResolve(t, r, "H", con("Pair", con("Char"), con("Bool")))
	assertFailure(t, res, NoInstance, "F", "Bool")
}

func TestCycleBeforeDepth(t *testing.T) {
	r := mustResolver(t, 2)
	mustAdd(t, r, "Cyc", con("Pair", vr(0), vr(1)),
		[]Constraint{{"Cyc", con("Pair", vr(1), vr(0))}})

	// Level 3 exceeds D=2, but the goal is also a cycle: cycle wins.
	res := mustResolve(t, r, "Cyc", con("Pair", con("Int"), con("Char")))
	assertFailure(t, res, Cycle, "Cyc", "Pair<Int,Char>")
}

func TestDepthBoundary(t *testing.T) {
	setup := func(d int) *Resolver {
		r := mustResolver(t, d)
		mustAdd(t, r, "P", con("Int"), nil)
		mustAdd(t, r, "P", con("List", vr(0)), []Constraint{{"P", vr(0)}})
		return r
	}

	// A goal at level exactly D is allowed.
	r := setup(3)
	res := mustResolve(t, r, "P", listN(con("Int"), 2))
	assertSuccess(t, res, 2, 3)

	// Level D+1 is rejected.
	r = setup(3)
	res = mustResolve(t, r, "P", listN(con("Int"), 3))
	assertFailure(t, res, DepthExceeded, "P", "Int")

	r = setup(1)
	res = mustResolve(t, r, "P", con("Int"))
	assertSuccess(t, res, 1, 1)
	res = mustResolve(t, r, "P", con("List", con("Int")))
	assertFailure(t, res, DepthExceeded, "P", "Int")
}

func TestLoopDepthExample(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Loop", vr(0), []Constraint{{"Loop", con("List", vr(0))}})

	res := mustResolve(t, r, "Loop", con("Int"))
	want := goalOf("Loop", listN(con("Int"), 8))
	assertFailure(t, res, DepthExceeded, "Loop", want.typ.String())
	if cacheSize(r) != 0 {
		t.Fatalf("failed resolution must not cache, cache size %d", cacheSize(r))
	}
}

func TestCycleExample(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Cyc", con("Pair", vr(0), vr(1)),
		[]Constraint{{"Cyc", con("Pair", vr(1), vr(0))}})

	res := mustResolve(t, r, "Cyc", con("Pair", con("Int"), con("Char")))
	assertFailure(t, res, Cycle, "Cyc", "Pair<Int,Char>")
}

func TestCacheDepthConditionNotHit(t *testing.T) {
	r := mustResolver(t, 3)
	mustAdd(t, r, "P", con("Int"), nil)
	mustAdd(t, r, "P", con("List", vr(0)), []Constraint{{"P", vr(0)}})

	res := mustResolve(t, r, "P", listN(con("Int"), 2))
	assertSuccess(t, res, 2, 3)
	// Cache: P(List^2<Int>) h3, P(List<Int>) h2, P(Int) h1.

	before := r.headMatches
	res = mustResolve(t, r, "P", listN(con("Int"), 3))
	// Levels 2 and 3 hold cached entries, but level+height-1 = 4 > D=3,
	// so they must not be used: all three levels scan both instances.
	assertFailure(t, res, DepthExceeded, "P", "Int")
	if delta := r.headMatches - before; delta != 6 {
		t.Fatalf("expected 6 head match attempts (no cache used), got %d", delta)
	}
}
