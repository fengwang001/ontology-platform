package inline

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func testCfg() Config {
	return Config{
		GlobalSizeLimit:     1000,
		GrowthMultiple:      1,
		GrowthMultipleDen:   1,
		CallOverhead:        1,
		MaxChainOccurrences: 3,
	}
}

func mustAdd(t *testing.T, r *Registry, f Func) {
	t.Helper()
	if err := r.Add(f); err != nil {
		t.Fatalf("Add(%s): %v", f.Name, err)
	}
}

// Budget: growth up to exactly the cap is allowed; one unit more is rejected.
func TestBudgetBoundaryEqualityAllowed(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 10, Sites: []Site{
		{Callee: "small", Heat: 1},
	}})
	mustAdd(t, r, Func{Name: "small", Size: 11}) // delta 10 -> 20, equality

	rep, err := r.Decide("main", testCfg(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FinalSize != 20 {
		t.Fatalf("equality boundary: final=%d want 20", rep.FinalSize)
	}
	if rep.Sites[0].Fate != FateInlined {
		t.Fatalf("equality boundary should be allowed, got %s", rep.Sites[0].Reason)
	}
}

func TestBudgetBoundaryOneOverRejected(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 10, Sites: []Site{
		{Callee: "small", Heat: 1},
	}})
	mustAdd(t, r, Func{Name: "small", Size: 12}) // delta 11 -> 21 > 20

	rep, err := r.Decide("main", testCfg(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FinalSize != 10 {
		t.Fatalf("over boundary: final=%d want 10", rep.FinalSize)
	}
	if got := rep.Sites[0].Reason; got != ReasonBudget {
		t.Fatalf("over boundary reason=%s want budget", got)
	}
}

// Always-inline breaches the budget, and the breach shifts every later check.
func TestAlwaysInlineBreachesAndCascades(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 10, Sites: []Site{
		{Callee: "forced", Heat: 1},
		{Callee: "extra", Heat: 9}, // examined first: 10 -> 11
	}})
	mustAdd(t, r, Func{Name: "extra", Size: 2})
	mustAdd(t, r, Func{Name: "forced", Size: 50, Flags: FlagAlwaysInline})

	rep, err := r.Decide("main", testCfg(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// extra: 10+1=11; forced(always): 11+49=60.
	if rep.FinalSize != 60 {
		t.Fatalf("final=%d want 60", rep.FinalSize)
	}

	r2 := NewRegistry()
	mustAdd(t, r2, Func{Name: "main", Size: 10, Sites: []Site{
		{Callee: "forced", Heat: 9}, // breach happens first
		{Callee: "extra", Heat: 1},  // fits from 10, not from 60
	}})
	mustAdd(t, r2, Func{Name: "extra", Size: 12})
	mustAdd(t, r2, Func{Name: "forced", Size: 50, Flags: FlagAlwaysInline})
	rep2, err := r2.Decide("main", testCfg(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rep2.Sites {
		if s.Callee == "extra" && s.Reason != ReasonBudget {
			t.Fatalf("cascade: extra after breach reason=%s want budget", s.Reason)
		}
	}
	if rep2.FinalSize != 59 {
		t.Fatalf("cascade final=%d want 59", rep2.FinalSize)
	}
}

// Mutual recursion allowed to the limit; siblings keep separate counts.
func TestMutualRecursionChainLimitAndSiblings(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 5, Sites: []Site{{Callee: "a", Heat: 1}}})
	mustAdd(t, r, Func{Name: "a", Size: 2, Sites: []Site{{Callee: "b", Heat: 1}}})
	mustAdd(t, r, Func{Name: "b", Size: 2, Sites: []Site{{Callee: "a", Heat: 1}}})

	cfg := testCfg()
	cfg.GlobalSizeLimit = 1_000_000
	cfg.GrowthMultiple = 1_000_000
	rep, err := r.Decide("main", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	// chain main-a-b-a-b-a ; the 4th a is the first limit violation.
	var rejected []string
	for _, s := range rep.Sites {
		if s.Fate == FateRejected {
			if s.Reason != ReasonChainExceeded {
				t.Fatalf("reason %s, want chain-exceeded", s.Reason)
			}
			rejected = append(rejected, s.Callee)
		}
	}
	if len(rejected) != 1 || rejected[0] != "a" {
		t.Fatalf("rejected=%v, want exactly [a]", rejected)
	}

	r2 := NewRegistry()
	mustAdd(t, r2, Func{Name: "main", Size: 5, Sites: []Site{
		{Callee: "a", Heat: 2},
		{Callee: "a", Heat: 1},
	}})
	mustAdd(t, r2, Func{Name: "a", Size: 2, Sites: []Site{
		{Callee: "leaf", Heat: 5},
		{Callee: "a", Heat: 1},
	}})
	mustAdd(t, r2, Func{Name: "leaf", Size: 2})
	rep2, err := r2.Decide("main", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf := 0
	for _, s := range rep2.Sites {
		if s.Callee == "leaf" && s.Fate == FateInlined {
			leaf++
		}
		if s.Callee == "a" && len(s.Path) > 1 && s.Reason != ReasonDirectRecursion {
			t.Fatalf("nested a: %+v want direct-recursion", s)
		}
	}
	if leaf != 2 {
		t.Fatalf("leaf inlined %d times, want 2 (sibling isolation)", leaf)
	}
}

func TestConflictingFlagsRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(Func{Name: "x", Size: 1, Flags: FlagBoth}); !errors.Is(err, ErrConflictingFlags) {
		t.Fatalf("got %v want ErrConflictingFlags", err)
	}
}

// Copied sites merge into the global heat ordering, never append at the end.
func TestInsertedSitesReorderedByHeat(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 100, Sites: []Site{
		{Callee: "outer", Heat: 5},
		{Callee: "late", Heat: 1},
	}})
	mustAdd(t, r, Func{Name: "outer", Size: 2, BaseHeat: 5, Sites: []Site{
		{Callee: "child", Heat: 100},
	}})
	mustAdd(t, r, Func{Name: "child", Size: 2})
	mustAdd(t, r, Func{Name: "late", Size: 2})

	cfg := testCfg()
	cfg.GlobalSizeLimit = 1_000_000
	cfg.GrowthMultiple = 1_000_000
	rep, err := r.Decide("main", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"outer", "child", "late"}
	if len(rep.Sites) != len(want) {
		t.Fatalf("sites=%d want %d", len(rep.Sites), len(want))
	}
	for i, w := range want {
		if rep.Sites[i].Callee != w {
			t.Fatalf("order=%v want %v", callees(rep.Sites), want)
		}
	}
}

func callees(ss []SiteFate) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Callee
	}
	return out
}

func TestReasonPriority(t *testing.T) {
	t.Run("undefined first", func(t *testing.T) {
		r := NewRegistry()
		mustAdd(t, r, Func{Name: "main", Size: 1, Sites: []Site{{Callee: "missing"}}})
		rep, _ := r.Decide("main", testCfg(), nil)
		if rep.Sites[0].Reason != ReasonUndefinedCallee {
			t.Fatalf("got %s", rep.Sites[0].Reason)
		}
	})
	t.Run("noinline beats direct recursion", func(t *testing.T) {
		r := NewRegistry()
		mustAdd(t, r, Func{Name: "main", Size: 1, Flags: FlagNoInline,
			Sites: []Site{{Callee: "main"}}})
		rep, _ := r.Decide("main", testCfg(), nil)
		if rep.Sites[0].Reason != ReasonNoInline {
			t.Fatalf("got %s", rep.Sites[0].Reason)
		}
	})
	t.Run("direct recursion beats structure", func(t *testing.T) {
		r := NewRegistry()
		mustAdd(t, r, Func{Name: "main", Size: 1, Uninlinable: true,
			Sites: []Site{{Callee: "main"}}})
		rep, _ := r.Decide("main", testCfg(), nil)
		if rep.Sites[0].Reason != ReasonDirectRecursion {
			t.Fatalf("got %s", rep.Sites[0].Reason)
		}
	})
	t.Run("structure beats chain limit", func(t *testing.T) {
		r := NewRegistry()
		mustAdd(t, r, Func{Name: "main", Size: 1, Sites: []Site{{Callee: "u"}}})
		mustAdd(t, r, Func{Name: "u", Size: 1, Uninlinable: true,
			Sites: []Site{{Callee: "u"}}})
		cfg := testCfg()
		cfg.MaxChainOccurrences = 1
		cfg.GlobalSizeLimit = 1_000_000
		cfg.GrowthMultiple = 1_000_000
		rep, _ := r.Decide("main", cfg, nil)
		if rep.Sites[0].Reason != ReasonUninlinableStructure {
			t.Fatalf("first u got %s want structure", rep.Sites[0].Reason)
		}
	})
	t.Run("chain beats budget", func(t *testing.T) {
		r := NewRegistry()
		mustAdd(t, r, Func{Name: "main", Size: 1, Sites: []Site{{Callee: "a"}}})
		mustAdd(t, r, Func{Name: "a", Size: 1, Sites: []Site{{Callee: "b"}}})
		mustAdd(t, r, Func{Name: "b", Size: 1, Sites: []Site{{Callee: "a"}}})
		rep, _ := r.Decide("main", testCfg(), nil)
		found := false
		for _, s := range rep.Sites {
			if s.Reason == ReasonChainExceeded {
				found = true
			}
		}
		if !found {
			t.Fatalf("no chain-exceeded among %v", callees(rep.Sites))
		}
	})
}

func TestConcurrentTasksIsolation(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 10, Sites: []Site{{Callee: "a", Heat: 1}}})
	mustAdd(t, r, Func{Name: "a", Size: 3, Sites: []Site{{Callee: "b", Heat: 1}}})
	mustAdd(t, r, Func{Name: "b", Size: 3})

	var wg sync.WaitGroup
	outs := make([]string, 64)
	errs := make(chan error, 64)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rep, err := r.Decide("main", testCfg(), nil)
			if err != nil {
				errs <- err
				return
			}
			outs[i] = rep.Render()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for i := 1; i < len(outs); i++ {
		if outs[i] != outs[0] {
			t.Fatalf("task %d differs from task 0", i)
		}
	}
	if !strings.Contains(outs[0], "final=14") {
		t.Fatalf("unexpected result:\n%s", outs[0])
	}
}

type hookLogger struct{ onInput func() }

func (h hookLogger) Logf(string, ...any) {
	if h.onInput != nil {
		h.onInput()
	}
}

// Mutation while a task runs is rejected (freeze policy), then allowed again.
func TestFreezeDuringTaskRejectsMutation(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 1, Sites: []Site{{Callee: "main"}}})

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := r.Decide("main", testCfg(), hookLogger{
			onInput: func() {
				once.Do(func() { close(entered) })
				<-release
			},
		})
		done <- err
	}()
	<-entered

	// While blocked inside the task, every logged line also blocks; ensure the
	// task cannot proceed past its frozen section while we mutate.
	if err := r.Add(Func{Name: "z", Size: 1}); !errors.Is(err, ErrRegistryFrozen) {
		t.Fatalf("add during task: got %v", err)
	}
	if err := r.Replace(Func{Name: "main", Size: 2}); !errors.Is(err, ErrRegistryFrozen) {
		t.Fatalf("replace during task: got %v", err)
	}
	if err := r.Remove("main"); !errors.Is(err, ErrRegistryFrozen) {
		t.Fatalf("remove during task: got %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Func{Name: "z", Size: 1}); err != nil {
		t.Fatalf("add after task: %v", err)
	}
}

func TestLoggingCoversInputBasisOutput(t *testing.T) {
	var sb strings.Builder
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 10, Sites: []Site{{Callee: "x", Heat: 2}}})
	mustAdd(t, r, Func{Name: "x", Size: 3})
	rep, err := r.Decide("main", testCfg(), logWriter{w: &sb})
	if err != nil {
		t.Fatal(err)
	}
	log := sb.String()
	for _, want := range []string{"input:", "basis", "INLINED", "output:", "final=12"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
	if !strings.Contains(rep.Render(), "deepest-chain main -> x") {
		t.Fatalf("deepest path wrong:\n%s", rep.Render())
	}
}

func TestDeterminismAcrossRuns(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, Func{Name: "main", Size: 12, Sites: []Site{
		{Callee: "p", Heat: 3}, {Callee: "q", Heat: 3},
	}})
	mustAdd(t, r, Func{Name: "p", Size: 4, Sites: []Site{{Callee: "q", Heat: 7}}})
	mustAdd(t, r, Func{Name: "q", Size: 4, Sites: []Site{{Callee: "p", Heat: 6}}})

	var first string
	for run := 0; run < 20; run++ {
		all, err := r.DecideAll(testCfg(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var got strings.Builder
		for _, rep := range sortedReports(all) {
			got.WriteString(rep.Render())
		}
		if run == 0 {
			first = got.String()
		} else if got.String() != first {
			t.Fatalf("run %d differs", run)
		}
	}
}
