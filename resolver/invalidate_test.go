package resolver

import (
	"fmt"
	"testing"
)

func TestInvalidationNoMatchKeepsAll(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", con("Int"), nil)
	mustAdd(t, r, "Show", con("List", vr(0)), []Constraint{{"Show", vr(0)}})
	res := mustResolve(t, r, "Show", con("List", con("Int")))
	assertSuccess(t, res, 2, 2)
	if cacheSize(r) != 2 {
		t.Fatalf("expected 2 cache entries, got %d", cacheSize(r))
	}

	// Head matches no cached goal: zero invalidation.
	mustAdd(t, r, "Show", con("Pair", vr(0), vr(1)), nil)
	if cacheSize(r) != 2 {
		t.Fatalf("expected zero invalidation, cache size %d", cacheSize(r))
	}
	if !cached(r, "Show", con("Int")) || !cached(r, "Show", con("List", con("Int"))) {
		t.Fatal("entries must be kept")
	}
}

func TestInvalidationKeepWhenSelectedMoreSpecialized(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", con("List", vr(0)), nil)             // 1
	mustAdd(t, r, "Show", con("List", con("Char")), nil)       // 2
	res := mustResolve(t, r, "Show", con("List", con("Char"))) // selects 2
	assertSuccess(t, res, 2, 1)

	// New instance with a bare variable head matches the cached goal, but
	// the selected instance 2 is more specialized than it: keep.
	mustAdd(t, r, "Show", vr(0), nil) // 3
	if !cached(r, "Show", con("List", con("Char"))) {
		t.Fatal("entry must be kept: selected instance is more specialized than the new one")
	}
	before := r.headMatches
	res = mustResolve(t, r, "Show", con("List", con("Char")))
	assertSuccess(t, res, 2, 1)
	if delta := r.headMatches - before; delta != 0 {
		t.Fatalf("kept entry must still be a cache hit, got %d match attempts", delta)
	}
}

func TestInvalidationDropWhenNewMoreSpecialized(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", con("List", vr(0)), nil) // 1
	res := mustResolve(t, r, "Show", con("List", con("Char")))
	assertSuccess(t, res, 1, 1)

	mustAdd(t, r, "Show", con("List", con("Char")), nil) // 2, more specialized
	if cacheSize(r) != 0 {
		t.Fatalf("entry must be invalidated, cache size %d", cacheSize(r))
	}
	res = mustResolve(t, r, "Show", con("List", con("Char")))
	assertSuccess(t, res, 2, 1)
}

func TestInvalidationDropWhenIncomparable(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Eq", con("Pair", con("Int"), vr(0)), nil) // 1
	res := mustResolve(t, r, "Eq", con("Pair", con("Int"), con("Int")))
	assertSuccess(t, res, 1, 1)

	mustAdd(t, r, "Eq", con("Pair", vr(0), con("Int")), nil) // 2, incomparable
	if cacheSize(r) != 0 {
		t.Fatalf("entry must be invalidated, cache size %d", cacheSize(r))
	}
	res = mustResolve(t, r, "Eq", con("Pair", con("Int"), con("Int")))
	assertFailure(t, res, Ambiguous, "Eq", "Pair<Int,Int>")
}

func TestInvalidationPropagatesToDependentsOnly(t *testing.T) {
	setup := func() *Resolver {
		r := mustResolver(t, 8)
		mustAdd(t, r, "A", con("List", vr(0)), nil)                                  // 1
		mustAdd(t, r, "A", listN(vr(0), 2), []Constraint{{"A", con("List", vr(0))}}) // 2
		res := mustResolve(t, r, "A", listN(con("Int"), 2))
		assertSuccess(t, res, 2, 2)
		return r
	}

	// Dooming the child goal dooms the parent whose tree contains it.
	r := setup()
	mustAdd(t, r, "A", con("List", con("Int")), nil) // 3, matches child goal
	if cacheSize(r) != 0 {
		t.Fatalf("both entries must be invalidated, cache size %d", cacheSize(r))
	}

	// Dooming the parent goal does not propagate towards the subgoal.
	r = setup()
	mustAdd(t, r, "A", listN(con("Int"), 2), nil) // 3, matches parent goal only
	if cached(r, "A", listN(con("Int"), 2)) {
		t.Fatal("parent entry must be invalidated")
	}
	if !cached(r, "A", con("List", con("Int"))) {
		t.Fatal("child entry must be kept")
	}
}

func TestFailureNotCachedButSuccessfulSubgoalsAre(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "T", con("Int"), nil) // 1
	mustAdd(t, r, "T", con("Pair", vr(0), vr(1)),
		[]Constraint{{"T", vr(0)}, {"T", vr(1)}}) // 2

	res := mustResolve(t, r, "T", con("Pair", con("Int"), con("Char")))
	assertFailure(t, res, NoInstance, "T", "Char")

	if cached(r, "T", con("Pair", con("Int"), con("Char"))) {
		t.Fatal("failed goal must not be cached")
	}
	if cached(r, "T", con("Char")) {
		t.Fatal("failed subgoal must not be cached")
	}
	if !cached(r, "T", con("Int")) {
		t.Fatal("successful subgoal must be cached")
	}
}

func TestDuplicateByRenaming(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Eq", con("Pair", vr(0), vr(1)), nil)
	if _, err := r.AddInstance("Eq", con("Pair", vr(2), vr(3)), nil); err != nil {
		assertReject(t, err, RejectDuplicateInstance)
	} else {
		t.Fatal("expected duplicate rejection for renamed variables")
	}
	mustAdd(t, r, "Eq", con("Pair", vr(2), vr(2)), nil)
	if _, err := r.AddInstance("Eq", con("Pair", vr(4), vr(4)), nil); err != nil {
		assertReject(t, err, RejectDuplicateInstance)
	} else {
		t.Fatal("expected duplicate rejection for renamed non-linear head")
	}
	// Same head under a different trait is not a duplicate.
	mustAdd(t, r, "F", con("Pair", vr(0), vr(1)), nil)
	// Same head with a different context is still a duplicate.
	if _, err := r.AddInstance("Eq", con("Pair", vr(1), vr(0)),
		[]Constraint{{"F", vr(0)}}); err != nil {
		assertReject(t, err, RejectDuplicateInstance)
	} else {
		t.Fatal("expected duplicate rejection regardless of context")
	}
}

func TestLimits(t *testing.T) {
	if _, err := NewResolver(1); err != nil {
		t.Fatalf("D=1 must be allowed: %v", err)
	}
	if _, err := NewResolver(64); err != nil {
		t.Fatalf("D=64 must be allowed: %v", err)
	}
	for _, d := range []int{0, -1, 65} {
		if _, err := NewResolver(d); err != nil {
			assertReject(t, err, RejectInvalidParam)
		} else {
			t.Fatalf("D=%d must be rejected", d)
		}
	}

	name32 := string(make([]byte, 32))
	name33 := string(make([]byte, 33))
	if _, err := Con(name32); err != nil {
		t.Fatalf("32-byte name must be allowed: %v", err)
	}
	if _, err := Con(""); err == nil {
		t.Fatal("empty name must be rejected")
	}
	if _, err := Con(name33); err == nil {
		t.Fatal("33-byte name must be rejected")
	}

	leaf := con("I")
	if _, err := Con("F", leaf, leaf, leaf, leaf); err != nil {
		t.Fatalf("4 arguments must be allowed: %v", err)
	}
	if _, err := Con("F", leaf, leaf, leaf, leaf, leaf); err == nil {
		t.Fatal("5 arguments must be rejected")
	}

	if _, err := Var(7); err != nil {
		t.Fatalf("Var(7) must be allowed: %v", err)
	}
	if _, err := Var(8); err == nil {
		t.Fatal("Var(8) must be rejected")
	}
	if _, err := Var(-1); err == nil {
		t.Fatal("Var(-1) must be rejected")
	}

	r := mustResolver(t, 8)
	ctx4 := []Constraint{{"F", vr(0)}, {"F", vr(0)}, {"F", vr(0)}, {"F", vr(0)}}
	if _, err := r.AddInstance("G", con("List", vr(0)), ctx4); err != nil {
		t.Fatalf("4 context constraints must be allowed: %v", err)
	}
	ctx5 := append(ctx4, Constraint{"F", vr(0)})
	if _, err := r.AddInstance("H", con("List", vr(0)), ctx5); err != nil {
		assertReject(t, err, RejectInvalidParam)
	} else {
		t.Fatal("5 context constraints must be rejected")
	}

	// Context variable absent from the head.
	if _, err := r.AddInstance("J", con("List", vr(0)),
		[]Constraint{{"F", vr(1)}}); err != nil {
		assertReject(t, err, RejectInvalidParam)
	} else {
		t.Fatal("context variable outside the head must be rejected")
	}

	// Ground type depth 16 allowed, 17 rejected.
	if _, err := r.Resolve("F", listN(con("Int"), 15)); err != nil {
		t.Fatalf("depth 16 must be allowed: %v", err)
	}
	if _, err := r.Resolve("F", listN(con("Int"), 16)); err != nil {
		assertReject(t, err, RejectInvalidParam)
	} else {
		t.Fatal("depth 17 must be rejected")
	}
	// Non-ground type rejected.
	if _, err := r.Resolve("F", con("List", vr(0))); err != nil {
		assertReject(t, err, RejectInvalidParam)
	} else {
		t.Fatal("type with variables must be rejected")
	}
	// Invalid trait names rejected.
	if _, err := r.Resolve("", con("Int")); err == nil {
		t.Fatal("empty trait name must be rejected")
	}
	if _, err := r.Resolve(name33, con("Int")); err == nil {
		t.Fatal("33-byte trait name must be rejected")
	}

	// Exactly 200 instances allowed, the 201st rejected.
	r2 := mustResolver(t, 8)
	for i := 1; i <= 200; i++ {
		id, err := r2.AddInstance("T", con(fmt.Sprintf("N%03d", i)), nil)
		if err != nil {
			t.Fatalf("instance %d must be allowed: %v", i, err)
		}
		if id != i {
			t.Fatalf("expected instance id %d, got %d", i, id)
		}
	}
	if _, err := r2.AddInstance("T", con("N201"), nil); err != nil {
		assertReject(t, err, RejectTooManyInstances)
	} else {
		t.Fatal("201st instance must be rejected")
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", con("Int"), nil)
	res := mustResolve(t, r, "Show", con("Int"))
	assertSuccess(t, res, 1, 1)

	matches := r.headMatches
	size := cacheSize(r)

	if _, err := r.AddInstance("", con("Char"), nil); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := r.AddInstance("Show", con("Int"), nil); err == nil {
		t.Fatal("expected duplicate rejection")
	}
	if _, err := r.AddInstance("Show", con("List", vr(0)),
		[]Constraint{{"Show", vr(1)}}); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := r.Resolve("Show", vr(0)); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := r.Resolve("Show", listN(con("Int"), 16)); err == nil {
		t.Fatal("expected rejection")
	}

	if len(r.instances) != 1 {
		t.Fatalf("instance count changed to %d", len(r.instances))
	}
	if cacheSize(r) != size || !cached(r, "Show", con("Int")) {
		t.Fatal("cache changed after rejected operations")
	}
	if r.headMatches != matches {
		t.Fatal("match counter changed after rejected operations")
	}
	// The next accepted instance keeps the expected number.
	id := mustAdd(t, r, "Show", con("Char"), nil)
	if id != 2 {
		t.Fatalf("expected instance id 2, got %d", id)
	}
}

func TestVerifyCacheConsistency(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", con("Int"), nil)
	mustAdd(t, r, "Show", con("List", vr(0)), []Constraint{{"Show", vr(0)}})
	mustResolve(t, r, "Show", listN(con("Int"), 3))
	mustAdd(t, r, "Show", con("List", con("Char")), nil)
	mustResolve(t, r, "Show", con("List", con("Char")))
	if err := r.VerifyCacheConsistency(); err != nil {
		t.Fatalf("consistency check failed: %v", err)
	}
}
