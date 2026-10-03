package resolver

import (
	"testing"
)

func TestCacheHitZeroMatchAttempts(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("Int"))
	mustAdd(t, r, "Show", Con("List", Var(0)), Constraint{Trait: "Show", Type: Var(0)})

	mustResolveOK(t, r, "Show", Con("List", Con("Int")))
	if !cacheHas(r, "Show", "List<Int>") || !cacheHas(r, "Show", "Int") {
		t.Fatal("expected goals to be cached")
	}

	before := r.matchAttempts
	tree := mustResolveOK(t, r, "Show", Con("List", Con("Int")))
	if r.matchAttempts != before {
		t.Fatalf("cache hit performed %d head match attempts, want 0", r.matchAttempts-before)
	}
	if tree.InstanceID != 2 || tree.Height() != 2 {
		t.Fatalf("got %v (height %d), want instance 2 height 2", tree, tree.Height())
	}
}

func TestCacheEntryInsufficientLevelIsNotAHit(t *testing.T) {
	r := mustResolver(t, 3)
	mustAdd(t, r, "Show", Con("Int"))                                                                // 1
	mustAdd(t, r, "Show", Con("List", Var(0)), Constraint{Trait: "Show", Type: Var(0)})              // 2
	mustAdd(t, r, "Show", Con("Pair", Var(0)), Constraint{Trait: "Show", Type: Con("List", Var(0))}) // 3
	mustAdd(t, r, "Show", Con("Tri", Var(0)), Constraint{Trait: "Show", Type: Con("Pair", Var(0))})  // 4

	// Cache {Show,Int} (height 1) and {Show,List<Int>} (height 2).
	mustResolveOK(t, r, "Show", Con("List", Con("Int")))
	// Level 2 with height 2 fits exactly: 2+2-1 = 3 <= D, a cache hit.
	tree := mustResolveOK(t, r, "Show", Con("Pair", Con("Int")))
	if tree.Height() != 3 {
		t.Fatalf("height: got %d, want 3", tree.Height())
	}
	if !cacheHas(r, "Show", "Pair<Int>") {
		t.Fatal("expected Pair<Int> to be cached")
	}

	// Now {Show,Pair<Int>} has height 3. At level 2 it does not fit
	// (2+3-1 = 4 > 3), so it is not a hit and instances are examined.
	before := r.matchAttempts
	f := mustResolveFail(t, r, "Show", Con("Tri", Con("Int")))
	if r.matchAttempts == before {
		t.Fatal("entry with insufficient level budget must not be a cache hit")
	}
	if f.Kind != FailDepthExceeded || f.Trait != "Show" || f.Type != "Int" {
		t.Fatalf("got %v, want depth-exceeded at Show<Int>", f)
	}
}

func TestAddInstanceNoMatchZeroInvalidation(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("Int"))
	mustAdd(t, r, "Show", Con("List", Var(0)), Constraint{Trait: "Show", Type: Var(0)})
	mustResolveOK(t, r, "Show", Con("List", Con("Int")))

	snapshot := map[goalKey]bool{}
	for key := range r.cache {
		snapshot[key] = true
	}

	// Different trait: cannot affect any Show entry.
	mustAdd(t, r, "Ord", Con("List", Var(0)))
	// Same trait but the head matches no cached goal type.
	mustAdd(t, r, "Show", Con("Maybe", Var(0)))

	if len(r.cache) != len(snapshot) {
		t.Fatalf("cache size changed: got %d, want %d", len(r.cache), len(snapshot))
	}
	for key := range snapshot {
		if r.cache[key] == nil {
			t.Fatalf("entry %v was wrongly invalidated", key)
		}
	}
}

func TestInvalidationKeepsEntryWhenSelectedIsMoreSpecific(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("List", Con("Char"))) // 1: S
	mustResolveOK(t, r, "Show", Con("List", Con("Char")))

	// N is strictly more general than S, so the entry survives.
	mustAdd(t, r, "Show", Con("List", Var(0))) // 2: N
	if !cacheHas(r, "Show", "List<Char>") {
		t.Fatal("entry must survive: selected instance is more specialized than the new one")
	}

	before := r.matchAttempts
	tree := mustResolveOK(t, r, "Show", Con("List", Con("Char")))
	if r.matchAttempts != before {
		t.Fatalf("expected a cache hit with 0 match attempts, got %d", r.matchAttempts-before)
	}
	if tree.InstanceID != 1 {
		t.Fatalf("got instance %d, want 1", tree.InstanceID)
	}
}

func TestInvalidationWhenNewInstanceIsMoreSpecific(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("List", Var(0))) // 1: S
	mustResolveOK(t, r, "Show", Con("List", Con("Int")))

	mustAdd(t, r, "Show", Con("List", Con("Int"))) // 2: N, more specific than S
	if cacheHas(r, "Show", "List<Int>") {
		t.Fatal("entry must be invalidated: new instance is more specialized")
	}
	tree := mustResolveOK(t, r, "Show", Con("List", Con("Int")))
	if tree.InstanceID != 2 || tree.Height() != 1 {
		t.Fatalf("got %v (height %d), want instance 2 height 1", tree, tree.Height())
	}
}

func TestInvalidationWhenIncomparable(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "F", Con("Pair", Var(0), Con("Int"))) // 1: S
	mustResolveOK(t, r, "F", Con("Pair", Con("Int"), Con("Int")))

	// Incomparable with S: the entry is invalidated and re-resolution is ambiguous.
	mustAdd(t, r, "F", Con("Pair", Con("Int"), Var(0))) // 2: N
	if cacheHas(r, "F", "Pair<Int,Int>") {
		t.Fatal("entry must be invalidated: instances are incomparable")
	}
	f := mustResolveFail(t, r, "F", Con("Pair", Con("Int"), Con("Int")))
	if f.Kind != FailAmbiguous {
		t.Fatalf("got %v, want ambiguity", f)
	}
}

func TestInvalidationPropagatesToDependentsNotChildren(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("Int"))                                                     // 1
	mustAdd(t, r, "F", Con("List", Var(0)), Constraint{Trait: "Show", Type: Var(0)})      // 2
	mustAdd(t, r, "G", Con("Int"), Constraint{Trait: "F", Type: Con("List", Con("Int"))}) // 3

	mustResolveOK(t, r, "F", Con("List", Con("Int")))
	mustResolveOK(t, r, "G", Con("Int"))
	for _, key := range []goalKey{{"Show", "Int"}, {"F", "List<Int>"}, {"G", "Int"}} {
		if r.cache[key] == nil {
			t.Fatalf("expected %v to be cached", key)
		}
	}

	// Invalidates {F,List<Int>} directly; the dependent {G,Int} follows,
	// while the child {Show,Int} survives.
	mustAdd(t, r, "F", Con("List", Con("Int"))) // 4
	if cacheHas(r, "F", "List<Int>") {
		t.Fatal("directly invalidated entry must be gone")
	}
	if cacheHas(r, "G", "Int") {
		t.Fatal("dependent entry must be invalidated")
	}
	if !cacheHas(r, "Show", "Int") {
		t.Fatal("invalidation must not propagate towards subgoals")
	}
	if err := r.selfCheck(); err != nil {
		t.Fatalf("selfCheck: %v", err)
	}
}

func TestFailureNotCachedButSuccessfulSubgoalsAre(t *testing.T) {
	r := mustResolver(t, 8)
	mustAdd(t, r, "Show", Con("Int")) // 1
	mustAdd(t, r, "Bad", Con("Int"),
		Constraint{Trait: "Show", Type: Con("Int")},
		Constraint{Trait: "Show", Type: Con("Char")},
	) // 2

	f := mustResolveFail(t, r, "Bad", Con("Int"))
	if f.Kind != FailNoInstance || f.Trait != "Show" || f.Type != "Char" {
		t.Fatalf("got %v, want no-instance at Show<Char>", f)
	}
	if cacheHas(r, "Bad", "Int") {
		t.Fatal("failed goals must not be cached")
	}
	if cacheHas(r, "Show", "Char") {
		t.Fatal("failed subgoals must not be cached")
	}
	if !cacheHas(r, "Show", "Int") {
		t.Fatal("subgoals that succeeded before the failure must be cached")
	}
}
