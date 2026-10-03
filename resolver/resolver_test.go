package resolver

import (
	"testing"
)

func mustResolver(t *testing.T, d int) *Resolver {
	t.Helper()
	r, err := NewResolver(d)
	if err != nil {
		t.Fatalf("NewResolver(%d): %v", d, err)
	}
	return r
}

func mustAdd(t *testing.T, r *Resolver, trait string, head *Type, ctx ...Constraint) int {
	t.Helper()
	id, err := r.AddInstance(trait, head, ctx)
	if err != nil {
		t.Fatalf("AddInstance(%s, %s): %v", trait, head, err)
	}
	return id
}

func mustResolveOK(t *testing.T, r *Resolver, trait string, ty *Type) *Tree {
	t.Helper()
	tree, failure, err := r.Resolve(trait, ty)
	if err != nil {
		t.Fatalf("Resolve(%s, %s): %v", trait, ty, err)
	}
	if failure != nil {
		t.Fatalf("Resolve(%s, %s): unexpected failure %v", trait, ty, failure)
	}
	return tree
}

func mustResolveFail(t *testing.T, r *Resolver, trait string, ty *Type) *Failure {
	t.Helper()
	tree, failure, err := r.Resolve(trait, ty)
	if err != nil {
		t.Fatalf("Resolve(%s, %s): %v", trait, ty, err)
	}
	if tree != nil {
		t.Fatalf("Resolve(%s, %s): unexpected success %v", trait, ty, tree)
	}
	return failure
}

func cacheHas(r *Resolver, trait, typ string) bool {
	return r.cache[goalKey{trait: trait, typ: typ}] != nil
}

func TestMatchNonLinearVariable(t *testing.T) {
	head := Con("Pair", Var(0), Var(0))
	if _, ok := match(head, Con("Pair", Con("Int"), Con("Char"))); ok {
		t.Fatal("Pair<a,a> must not match Pair<Int,Char>")
	}
	subst, ok := match(head, Con("Pair", Con("Int"), Con("Int")))
	if !ok {
		t.Fatal("Pair<a,a> must match Pair<Int,Int>")
	}
	if got := subst[0].String(); got != "Int" {
		t.Fatalf("substitution for a: got %s, want Int", got)
	}
}

func TestMoreSpecializedDirection(t *testing.T) {
	listA := Con("List", Var(0))
	listChar := Con("List", Con("Char"))
	if !moreSpecialized(listChar, listA) {
		t.Fatal("List<Char> must be more specialized than List<a>")
	}
	if moreSpecialized(listA, listChar) {
		t.Fatal("List<a> must not be more specialized than List<Char>")
	}
	if !moreSpecialized(listA, listA) {
		t.Fatal("a head is more specialized than itself")
	}

	pairIntA := Con("Pair", Con("Int"), Var(0))
	pairAInt := Con("Pair", Var(0), Con("Int"))
	if moreSpecialized(pairIntA, pairAInt) || moreSpecialized(pairAInt, pairIntA) {
		t.Fatal("Pair<Int,a> and Pair<a,Int> must be incomparable")
	}

	pairAA := Con("Pair", Var(0), Var(0))
	if moreSpecialized(pairAA, pairIntA) || moreSpecialized(pairIntA, pairAA) {
		t.Fatal("Pair<a,a> and Pair<Int,a> must be incomparable")
	}
	if moreSpecialized(pairAA, pairAInt) || moreSpecialized(pairAInt, pairAA) {
		t.Fatal("Pair<a,a> and Pair<a,Int> must be incomparable")
	}

	pairIntInt := Con("Pair", Con("Int"), Con("Int"))
	if !moreSpecialized(pairIntInt, pairIntA) || !moreSpecialized(pairIntInt, pairAInt) {
		t.Fatal("Pair<Int,Int> must be more specialized than both Pair<Int,a> and Pair<a,Int>")
	}
	if !moreSpecialized(pairIntInt, pairAA) {
		t.Fatal("Pair<Int,Int> must be more specialized than Pair<a,a>")
	}
	if moreSpecialized(pairAA, pairIntInt) {
		t.Fatal("Pair<a,a> must not be more specialized than Pair<Int,Int>")
	}
}

// The worked example from the specification.
func TestPromptMainExample(t *testing.T) {
	r := mustResolver(t, 8)
	if id := mustAdd(t, r, "Show", Con("Int")); id != 1 {
		t.Fatalf("instance id: got %d, want 1", id)
	}
	if id := mustAdd(t, r, "Show", Con("List", Var(0)), Constraint{Trait: "Show", Type: Var(0)}); id != 2 {
		t.Fatalf("instance id: got %d, want 2", id)
	}
	if id := mustAdd(t, r, "Show", Con("List", Con("Char"))); id != 3 {
		t.Fatalf("instance id: got %d, want 3", id)
	}

	// Resolve(Show, List<Char>): candidates 2 and 3, 3 is more specific.
	tree := mustResolveOK(t, r, "Show", Con("List", Con("Char")))
	if tree.InstanceID != 3 || tree.Height() != 1 {
		t.Fatalf("got %v (height %d), want instance 3 height 1", tree, tree.Height())
	}

	// Resolve(Show, List<Int>): only 2 matches, context Show(Int) uses 1.
	tree = mustResolveOK(t, r, "Show", Con("List", Con("Int")))
	if tree.InstanceID != 2 || tree.Height() != 2 {
		t.Fatalf("got %v (height %d), want instance 2 height 2", tree, tree.Height())
	}
	if len(tree.Children) != 1 || tree.Children[0].InstanceID != 1 {
		t.Fatalf("child of List<Int> derivation: got %v", tree.Children)
	}

	// Resolve(Show, List<List<Char>>): only 2 matches, subgoal hits the cache.
	tree = mustResolveOK(t, r, "Show", Con("List", Con("List", Con("Char"))))
	if tree.InstanceID != 2 || tree.Height() != 2 {
		t.Fatalf("got %v (height %d), want instance 2 height 2", tree, tree.Height())
	}
	if len(tree.Children) != 1 || tree.Children[0].InstanceID != 3 {
		t.Fatalf("child of List<List<Char>> derivation: got %v", tree.Children)
	}
	if got := len(r.cache); got != 4 {
		t.Fatalf("cache size: got %d, want 4", got)
	}

	// A full cache hit performs no head match attempts.
	before := r.matchAttempts
	mustResolveOK(t, r, "Show", Con("List", Con("Char")))
	if r.matchAttempts != before {
		t.Fatalf("cache hit performed %d head match attempts, want 0", r.matchAttempts-before)
	}

	// Instance 4: Show(List<List<a>>) with context [Show(a)].
	if id := mustAdd(t, r, "Show", Con("List", Con("List", Var(0))), Constraint{Trait: "Show", Type: Var(0)}); id != 4 {
		t.Fatalf("instance id: got %d, want 4", id)
	}
	// Only the List<List<Char>> entry is invalidated (instance 2 is not more
	// specialized than instance 4); all other entries survive.
	if cacheHas(r, "Show", "List<List<Char>>") {
		t.Fatal("List<List<Char>> entry must be invalidated")
	}
	for _, typ := range []string{"List<Char>", "Int", "List<Int>"} {
		if !cacheHas(r, "Show", typ) {
			t.Fatalf("Show<%s> entry must survive", typ)
		}
	}

	// Re-resolution selects instance 4, its context Show(Char) has no
	// instance, and there is no backtracking to instance 2.
	f := mustResolveFail(t, r, "Show", Con("List", Con("List", Con("Char"))))
	if f.Kind != FailNoInstance || f.Trait != "Show" || f.Type != "Char" {
		t.Fatalf("got %v, want no-instance failure at Show<Char>", f)
	}
	if err := r.selfCheck(); err != nil {
		t.Fatalf("selfCheck: %v", err)
	}
}

func TestAmbiguityAndResolution(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Eq", Con("Pair", Con("Int"), Var(0)))
	mustAdd(t, r, "Eq", Con("Pair", Var(0), Con("Int")))

	f := mustResolveFail(t, r, "Eq", Con("Pair", Con("Int"), Con("Int")))
	if f.Kind != FailAmbiguous || f.Trait != "Eq" || f.Type != "Pair<Int,Int>" {
		t.Fatalf("got %v, want ambiguity at Eq<Pair<Int,Int>>", f)
	}

	// A non-linear head is incomparable with both and keeps the ambiguity.
	mustAdd(t, r, "Eq", Con("Pair", Var(0), Var(0)))
	f = mustResolveFail(t, r, "Eq", Con("Pair", Con("Int"), Con("Int")))
	if f.Kind != FailAmbiguous {
		t.Fatalf("got %v, want ambiguity", f)
	}

	// Registering a strictly more specific instance resolves the ambiguity.
	mustAdd(t, r, "Eq", Con("Pair", Con("Int"), Con("Int")))
	tree := mustResolveOK(t, r, "Eq", Con("Pair", Con("Int"), Con("Int")))
	if tree.InstanceID != 4 || tree.Height() != 1 {
		t.Fatalf("got %v (height %d), want instance 4 height 1", tree, tree.Height())
	}
}

func TestContextOrderDeterminesFailure(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "F", Con("Pair", Var(0), Var(1)), Constraint{Trait: "F3", Type: Var(1)})
	mustAdd(t, r, "G", Con("Int"),
		Constraint{Trait: "F", Type: Con("Pair", Con("Int"), Con("Char"))},
		Constraint{Trait: "F2", Type: Con("Char")},
	)
	// Depth-first in declaration order: the first context F(Pair<Int,Char>)
	// is explored and fails at F3<Char> before F2(Char) is considered.
	f := mustResolveFail(t, r, "G", Con("Int"))
	if f.Kind != FailNoInstance || f.Trait != "F3" || f.Type != "Char" {
		t.Fatalf("got %v, want no-instance failure at F3<Char>", f)
	}
}

func TestDepthLimitBoundary(t *testing.T) {
	r := mustResolver(t, 2)
	mustAdd(t, r, "Show", Con("Int"))
	mustAdd(t, r, "Show", Con("List", Var(0)), Constraint{Trait: "Show", Type: Var(0)})

	// Level exactly D is allowed.
	tree := mustResolveOK(t, r, "Show", Con("List", Con("Int")))
	if tree.Height() != 2 {
		t.Fatalf("height: got %d, want 2", tree.Height())
	}
	// Level D+1 is rejected.
	f := mustResolveFail(t, r, "Show", Con("List", Con("List", Con("Int"))))
	if f.Kind != FailDepthExceeded || f.Trait != "Show" || f.Type != "Int" {
		t.Fatalf("got %v, want depth-exceeded at Show<Int>", f)
	}
}

func TestCycleCheckedBeforeDepth(t *testing.T) {
	r := mustResolver(t, 1)
	mustAdd(t, r, "C", Var(0), Constraint{Trait: "C", Type: Var(0)})
	// At level 2 the goal C(Int) is both beyond the depth limit and a
	// repetition of the ancestor goal; the cycle is reported.
	f := mustResolveFail(t, r, "C", Con("Int"))
	if f.Kind != FailCycle || f.Trait != "C" || f.Type != "Int" {
		t.Fatalf("got %v, want cycle at C<Int>", f)
	}
}

func TestDepthExample(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Loop", Var(0), Constraint{Trait: "Loop", Type: Con("List", Var(0))})
	f := mustResolveFail(t, r, "Loop", Con("Int"))
	want := "List<List<List<List<List<List<List<List<Int>>>>>>>>"
	if f.Kind != FailDepthExceeded || f.Trait != "Loop" || f.Type != want {
		t.Fatalf("got %v, want depth-exceeded at Loop<%s>", f, want)
	}
}

func TestCycleExample(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Cyc",
		Con("Pair", Var(0), Var(1)),
		Constraint{Trait: "Cyc", Type: Con("Pair", Var(1), Var(0))},
	)
	f := mustResolveFail(t, r, "Cyc", Con("Pair", Con("Int"), Con("Char")))
	if f.Kind != FailCycle || f.Trait != "Cyc" || f.Type != "Pair<Int,Char>" {
		t.Fatalf("got %v, want cycle at Cyc<Pair<Int,Char>>", f)
	}
}

func TestTopLevelVsSubgoalFailure(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("Int"))

	// Top-level goal without candidates: the failing goal is the query.
	f := mustResolveFail(t, r, "Show", Con("Char"))
	if f.Kind != FailNoInstance || f.Trait != "Show" || f.Type != "Char" {
		t.Fatalf("got %v, want no-instance at Show<Char>", f)
	}

	// Subgoal without candidates: the failing goal differs from the query.
	mustAdd(t, r, "H", Con("Int"), Constraint{Trait: "Show", Type: Con("Char")})
	f = mustResolveFail(t, r, "H", Con("Int"))
	if f.Kind != FailNoInstance || f.Trait != "Show" || f.Type != "Char" {
		t.Fatalf("got %v, want no-instance at subgoal Show<Char>", f)
	}
}
