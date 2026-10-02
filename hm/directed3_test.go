package hm

import "testing"

// Unifying two already-equivalent variables with a smaller level arriving
// later must propagate the minimum over the whole merged component.
func TestLevelPropagatesAcrossMergedComponent(t *testing.T) {
	s := mustNew(t, baseCtors(), 20)
	s.Enter()
	s.Enter()
	s.NewVar() // 1 at 2
	s.NewVar() // 2 at 2
	s.NewVar() // 3 at 2
	mustOK(t, s.Unify(Var{2}, Var{3}), "2=3")
	s.Leave()
	s.Leave()  // L=0
	s.NewVar() // 4 at 0
	mustOK(t, s.Unify(Var{4}, Var{3}), "0-level var merges with component")
	for _, id := range []int{2, 3, 4} {
		if l, _ := s.Level(id); l != 0 {
			t.Fatalf("Var%d level = %d, want 0 after merge", id, l)
		}
	}
}

// A later unify involving two already-identical variables re-harmonizes
// levels (the smaller operand level spreads through aliases).
func TestSelfUnifyRefreshesLevel(t *testing.T) {
	s := mustNew(t, baseCtors(), 20)
	s.Enter()
	s.Enter()
	s.NewVar() // 1 at 2
	s.NewVar() // 2 at 2
	mustOK(t, s.Unify(Var{2}, Var{1}), "2=1")
	s.Leave()
	s.Leave()
	mustOK(t, s.Unify(Var{1}, Var{2}), "1=2 again")
	// After the second unify both operands reach the same component; levels
	// remain 2 (no smaller level arrived).
	if l, _ := s.Level(1); l != 2 {
		t.Fatalf("level1 = %d", l)
	}
}

// Expensive Bind lowers aliases, not just the resolved root.
func TestExpensiveLowersAliasChain(t *testing.T) {
	s := mustNew(t, baseCtors(), 20)
	s.Enter()
	s.NewVar() // 1 at 1
	s.NewVar() // 2 at 1
	mustOK(t, s.Unify(Var{2}, Var{1}), "2->1")
	s.Leave() // L=0
	mustOK(t, s.Bind("a", Var{2}, true), "expensive bind of alias")
	if l, _ := s.Level(1); l != 0 {
		t.Fatalf("root level1 = %d, want 0", l)
	}
	if l, _ := s.Level(2); l != 0 {
		t.Fatalf("alias level2 = %d, want 0", l)
	}
}

// Constructor unification lowers variables nested beyond an alias chain.
func TestConstructorLoweringThroughAlias(t *testing.T) {
	s := mustNew(t, baseCtors(), 20)
	s.Enter()
	s.NewVar() // 1 at 1
	s.NewVar() // 2 at 1
	s.NewVar() // 3 at 1
	s.Leave()
	s.NewVar() // 4 at 0
	// Alias 1 -> 2 (both at 1), then bind a level-0 var's constructor so the
	// inner variable 3 reached through the term is lowered.
	mustOK(t, s.Unify(Var{4}, Con{"List", []Type{Var{3}}}), "4=List(3)")
	if l, _ := s.Level(3); l != 0 {
		t.Fatalf("level3 = %d, want 0", l)
	}
}
