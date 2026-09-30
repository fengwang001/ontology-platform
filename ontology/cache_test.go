package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustDecide(t *testing.T, cache *DecisionCache, subject, action, path string) Decision {
	t.Helper()
	decision, err := cache.Decide(subject, action, path)
	if err != nil {
		t.Fatalf("Decide(%q, %q, %q) error: %v", subject, action, path, err)
	}
	t.Logf("Decide input={subject:%q action:%q path:%q} output={effect:%q basis:%q noBasis:%t}",
		subject, action, path, decision.Effect, decision.BasisPath, decision.HasNoBasis)
	return decision
}

func mustSetRule(t *testing.T, cache *DecisionCache, path, subject, action string, effect Effect) {
	t.Helper()
	if err := cache.SetRule(path, subject, action, effect); err != nil {
		t.Fatalf("SetRule(%q, %q, %q, %q): %v", path, subject, action, effect, err)
	}
	t.Logf("SetRule input={path:%q subject:%q action:%q effect:%q} output=nil invalidationBasis={path:%q subject:%q action:%q}",
		path, subject, action, effect, path, subject, action)
}

func mustDeleteRule(t *testing.T, cache *DecisionCache, path, subject, action string) {
	t.Helper()
	if err := cache.DeleteRule(path, subject, action); err != nil {
		t.Fatalf("DeleteRule(%q, %q, %q): %v", path, subject, action, err)
	}
	t.Logf("DeleteRule input={path:%q subject:%q action:%q} output=nil invalidationBasis={path:%q subject:%q action:%q}",
		path, subject, action, path, subject, action)
}

func TestNearestAncestorDecision(t *testing.T) {
	cache := NewDecisionCache(10)
	if err := cache.SetRule("/", "alice", "read", Allow); err != nil {
		t.Fatalf("set root rule: %v", err)
	}
	if err := cache.SetRule("/a", "alice", "read", Deny); err != nil {
		t.Fatalf("set /a rule: %v", err)
	}

	if decision := mustDecide(t, cache, "alice", "read", "/a/b"); decision.Effect != Deny || decision.BasisPath != "/a" {
		t.Fatalf("decision = %+v, want deny based on /a", decision)
	}
	if decision := mustDecide(t, cache, "alice", "read", "/ab"); decision.Effect != Allow || decision.BasisPath != "/" {
		t.Fatalf("decision = %+v, want allow based on /", decision)
	}
	if decision := mustDecide(t, cache, "bob", "read", "/a"); decision.Effect != Deny || !decision.HasNoBasis {
		t.Fatalf("decision = %+v, want default deny without basis", decision)
	}
}

func TestValidationReportsFirstReason(t *testing.T) {
	emptyCache := NewDecisionCache(0)
	fullCache := NewDecisionCache(1)
	availableCache := NewDecisionCache(1)
	mustSetRule(t, fullCache, "/existing", "alice", "read", Allow)
	cases := []struct {
		name    string
		path    string
		subject string
		action  string
		effect  Effect
		cache   *DecisionCache
		want    error
	}{
		{name: "path", path: "a", subject: "alice", action: "read", effect: Allow, cache: emptyCache, want: ErrInvalidPath},
		{name: "empty segment", path: "/a//b", subject: "alice", action: "read", effect: Allow, cache: emptyCache, want: ErrInvalidPath},
		{name: "trailing slash", path: "/a/", subject: "alice", action: "read", effect: Allow, cache: emptyCache, want: ErrInvalidPath},
		{name: "subject", path: "/a", subject: "", action: "read", effect: Allow, cache: emptyCache, want: ErrEmptySubject},
		{name: "action", path: "/a", subject: "alice", action: "", effect: Allow, cache: emptyCache, want: ErrEmptyAction},
		{name: "effect", path: "/a", subject: "alice", action: "read", effect: "write", cache: availableCache, want: ErrInvalidEffect},
		{name: "limit before effect", path: "/a", subject: "alice", action: "read", effect: "write", cache: fullCache, want: ErrRuleLimitReached},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cache.SetRule(tc.path, tc.subject, tc.action, tc.effect)
			if !errors.Is(err, tc.want) {
				t.Fatalf("SetRule error = %v, want %v", err, tc.want)
			}
		})
	}

	if err := emptyCache.DeleteRule("/a", "alice", "read"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("DeleteRule error = %v, want %v", err, ErrRuleNotFound)
	}
}

func assertDecision(t *testing.T, cache *DecisionCache, path string, want Decision) {
	t.Helper()
	got := mustDecide(t, cache, "alice", "read", path)
	if got != want {
		t.Fatalf("decision for %q = %+v, want %+v", path, got, want)
	}
}

func TestDeepRuleInvalidatesOnlyAffectedBasisTuples(t *testing.T) {
	cache := NewDecisionCache(20)

	assertDecision(t, cache, "/a/b/c", Decision{Effect: Deny, HasNoBasis: true})
	assertDecision(t, cache, "/a/b/d", Decision{Effect: Deny, HasNoBasis: true})
	assertDecision(t, cache, "/a/x", Decision{Effect: Deny, HasNoBasis: true})
	assertDecision(t, cache, "/ab", Decision{Effect: Deny, HasNoBasis: true})

	if err := cache.SetRule("/a/b", "alice", "read", Allow); err != nil {
		t.Fatalf("set deep rule: %v", err)
	}
	if stats := cache.Stats(); stats.Invalidated != 2 {
		t.Fatalf("invalidated = %d, want 2; stats=%+v", stats.Invalidated, stats)
	}

	assertDecision(t, cache, "/a/b/c", Decision{Effect: Allow, BasisPath: "/a/b"})
	assertDecision(t, cache, "/a/b/d", Decision{Effect: Allow, BasisPath: "/a/b"})
	assertDecision(t, cache, "/a/x", Decision{Effect: Deny, HasNoBasis: true})
	assertDecision(t, cache, "/ab", Decision{Effect: Deny, HasNoBasis: true})

	if err := cache.SetRule("/a/b/c", "alice", "read", Deny); err != nil {
		t.Fatalf("set deeper rule: %v", err)
	}
	if stats := cache.Stats(); stats.Invalidated != 3 {
		t.Fatalf("invalidated = %d, want 3; stats=%+v", stats.Invalidated, stats)
	}

	assertDecision(t, cache, "/a/b/c", Decision{Effect: Deny, BasisPath: "/a/b/c"})
	assertDecision(t, cache, "/a/b/d", Decision{Effect: Allow, BasisPath: "/a/b"})

	if err := cache.DeleteRule("/a/b/c", "alice", "read"); err != nil {
		t.Fatalf("delete deeper rule: %v", err)
	}
	if stats := cache.Stats(); stats.Invalidated != 4 {
		t.Fatalf("invalidated = %d, want 4; stats=%+v", stats.Invalidated, stats)
	}
	assertDecision(t, cache, "/a/b/c", Decision{Effect: Allow, BasisPath: "/a/b"})

	if err := cache.DeleteRule("/a/b", "alice", "read"); err != nil {
		t.Fatalf("delete deep rule: %v", err)
	}
	if stats := cache.Stats(); stats.Invalidated != 6 {
		t.Fatalf("invalidated = %d, want 6; stats=%+v", stats.Invalidated, stats)
	}
	assertDecision(t, cache, "/a/b/c", Decision{Effect: Deny, HasNoBasis: true})
	assertDecision(t, cache, "/a/b/d", Decision{Effect: Deny, HasNoBasis: true})
}

func TestSameValueOverwriteInvalidatesNothing(t *testing.T) {
	cache := NewDecisionCache(10)
	if err := cache.SetRule("/a", "alice", "read", Allow); err != nil {
		t.Fatalf("set rule: %v", err)
	}
	assertDecision(t, cache, "/a", Decision{Effect: Allow, BasisPath: "/a"})
	assertDecision(t, cache, "/a/b", Decision{Effect: Allow, BasisPath: "/a"})
	assertDecision(t, cache, "/ab", Decision{Effect: Deny, HasNoBasis: true})

	before := cache.Stats()
	if err := cache.SetRule("/a", "alice", "read", Allow); err != nil {
		t.Fatalf("overwrite same rule: %v", err)
	}
	after := cache.Stats()
	if after.Invalidated != before.Invalidated {
		t.Fatalf("invalidated changed from %d to %d, want no invalidation", before.Invalidated, after.Invalidated)
	}
	assertDecision(t, cache, "/a", Decision{Effect: Allow, BasisPath: "/a"})
	assertDecision(t, cache, "/a/b", Decision{Effect: Allow, BasisPath: "/a"})
	assertDecision(t, cache, "/ab", Decision{Effect: Deny, HasNoBasis: true})
	if stats := cache.Stats(); stats.Hits-before.Hits != 3 {
		t.Fatalf("new hits = %d, want 3 cached reads", stats.Hits-before.Hits)
	}
}

func TestConcurrentDecisionBackfillAndRuleChanges(t *testing.T) {
	cache := NewDecisionCache(10)
	if err := cache.SetRule("/", "alice", "read", Allow); err != nil {
		t.Fatalf("set root rule: %v", err)
	}

	var paths []string
	for i := 0; i < 64; i++ {
		paths = append(paths, fmt.Sprintf("/res/%d/child", i))
	}

	stop := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for round := 0; ; round++ {
				select {
				case <-stop:
					return
				default:
				}

				path := paths[(worker+round)%len(paths)]
				decision, err := cache.Decide("alice", "read", path)
				if err != nil {
					t.Errorf("concurrent Decide(%q): %v", path, err)
					return
				}
				if decision.Effect != Allow && decision.Effect != Deny {
					t.Errorf("invalid effect %q for %q", decision.Effect, path)
					return
				}
				if decision.Effect == Allow && decision.BasisPath != "/" {
					t.Errorf("unexpected basis %q for allow decision on %q", decision.BasisPath, path)
					return
				}
				if decision.Effect == Deny && decision.BasisPath != "/res" {
					t.Errorf("unexpected basis %q for deny decision on %q", decision.BasisPath, path)
					return
				}
				t.Logf("Concurrent Decide input={subject:\"alice\" action:\"read\" path:%q} output={effect:%q basis:%q}",
					path, decision.Effect, decision.BasisPath)
			}
		}(worker)
	}

	for round := 0; round < 100; round++ {
		if round%2 == 0 {
			if err := cache.SetRule("/res", "alice", "read", Deny); err != nil {
				t.Fatalf("set denial rule: %v", err)
			}
		} else {
			if err := cache.DeleteRule("/res", "alice", "read"); err != nil {
				t.Fatalf("delete denial rule: %v", err)
			}
		}
	}

	close(stop)
	workers.Wait()
	if t.Failed() {
		return
	}

	if err := cache.DeleteRule("/res", "alice", "read"); err != nil && !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("cleanup denial rule: %v", err)
	}

	cache.mu.RLock()
	for key, entry := range cache.entries {
		want := cache.decideFromRules(key.subject, key.action, key.path)
		cache.mu.RUnlock()
		if entry.decision != want {
			t.Fatalf("stale cached %+v = %+v, want %+v", key, entry.decision, want)
		}
		cache.mu.RLock()
	}
	cache.mu.RUnlock()

	stats := cache.Stats()
	if stats.Hits < 0 || stats.Computations <= 0 || stats.Invalidated < 0 {
		t.Fatalf("invalid stats: %+v", stats)
	}
	t.Logf("Concurrent stats input={workers:8 paths:%d rounds:100} output=%+v", len(paths), stats)
}

func TestRuleLimitAndRejectionHaveNoSideEffects(t *testing.T) {
	cache := NewDecisionCache(1)
	mustSetRule(t, cache, "/a", "alice", "read", Allow)
	assertDecision(t, cache, "/a", Decision{Effect: Allow, BasisPath: "/a"})

	before := cache.Stats()
	if err := cache.SetRule("/a", "alice", "read", Allow); err != nil {
		t.Fatalf("overwriting existing rule with same value: %v", err)
	}
	if err := cache.SetRule("/b", "alice", "read", Allow); !errors.Is(err, ErrRuleLimitReached) {
		t.Fatalf("second rule error = %v, want %v", err, ErrRuleLimitReached)
	}
	if err := cache.SetRule("invalid", "", "", "bad"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("invalid operation error = %v, want %v", err, ErrInvalidPath)
	}
	if err := cache.DeleteRule("/missing", "alice", "read"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("missing delete error = %v, want %v", err, ErrRuleNotFound)
	}

	after := cache.Stats()
	if after != before {
		t.Fatalf("stats changed after rejected operations: before=%+v after=%+v", before, after)
	}
	assertDecision(t, cache, "/b", Decision{Effect: Deny, HasNoBasis: true})
	if stats := cache.Stats(); stats.Hits != before.Hits {
		t.Fatalf("first /b lookup unexpectedly hit cache: stats=%+v", stats)
	}
}

func TestInvalidationIsScopedToSubjectAndAction(t *testing.T) {
	cache := NewDecisionCache(10)
	mustSetRule(t, cache, "/", "alice", "read", Allow)
	assertDecision(t, cache, "/a", Decision{Effect: Allow, BasisPath: "/"})

	before := cache.Stats()
	mustSetRule(t, cache, "/", "bob", "write", Deny)
	assertDecision(t, cache, "/a", Decision{Effect: Allow, BasisPath: "/"})
	after := cache.Stats()
	if after.Invalidated != before.Invalidated {
		t.Fatalf("different subject/action invalidated %d entries, want 0", after.Invalidated-before.Invalidated)
	}
	if after.Hits-before.Hits != 1 {
		t.Fatalf("alice/read lookup hits = %d, want 1", after.Hits-before.Hits)
	}

	mustDeleteRule(t, cache, "/", "bob", "write")
	assertDecision(t, cache, "/a", Decision{Effect: Allow, BasisPath: "/"})
	if stats := cache.Stats(); stats.Invalidated != before.Invalidated {
		t.Fatalf("different subject/action delete invalidated entries, total=%d", stats.Invalidated)
	}
}

func TestConcurrentMissesWithMutationDoNotBackfillStaleResult(t *testing.T) {
	cache := NewDecisionCache(10)
	mustSetRule(t, cache, "/", "alice", "read", Allow)

	start := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 32; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			decision, err := cache.Decide("alice", "read", "/a")
			if err != nil {
				t.Errorf("concurrent decide: %v", err)
				return
			}
			if decision.BasisPath != "/" || (decision.Effect != Allow && decision.Effect != Deny) {
				t.Errorf("invalid interleaved decision: %+v", decision)
			}
		}()
	}

	close(start)
	for round := 0; round < 64; round++ {
		if round%2 == 0 {
			mustSetRule(t, cache, "/", "alice", "read", Deny)
		} else {
			mustSetRule(t, cache, "/", "alice", "read", Allow)
		}
	}
	readers.Wait()
	if t.Failed() {
		return
	}

	mustSetRule(t, cache, "/", "alice", "read", Deny)
	assertDecision(t, cache, "/a", Decision{Effect: Deny, BasisPath: "/"})
}

func TestBackfillAfterVersionChangeUsesCurrentRule(t *testing.T) {
	cache := NewDecisionCache(10)
	mustSetRule(t, cache, "/", "alice", "read", Allow)

	cache.mu.RLock()
	version := cache.version
	oldDecision := cache.decideFromRules("alice", "read", "/a")
	cache.mu.RUnlock()

	cache.mu.Lock()
	cache.rules[ruleKey{path: "/", subject: "alice", action: "read"}] = Deny
	cache.version++
	cache.mu.Unlock()

	key := cacheKey{subject: "alice", action: "read", path: "/a"}
	decision, err := cache.backfill(key, version, oldDecision)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if decision.Effect != Deny || decision.BasisPath != "/" {
		t.Fatalf("backfilled decision = %+v, want current deny from /", decision)
	}

	cache.mu.RLock()
	cached := cache.entries[key]
	cache.mu.RUnlock()
	if cached.decision != decision {
		t.Fatalf("cached decision = %+v, want %+v", cached.decision, decision)
	}
}
