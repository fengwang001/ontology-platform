package depsolver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

type bufLogger struct{ buf bytes.Buffer }

func (l *bufLogger) Logf(format string, args ...any) {
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func mustPublish(t *testing.T, r *Repository, name, version string, deps map[string]string) {
	t.Helper()
	if err := r.Publish(PackageVersion{Name: name, Version: version, Dependencies: deps}); err != nil {
		t.Fatalf("publish %s@%s: %v", name, version, err)
	}
}

func solveParsed(t *testing.T, r *Repository, root map[string]string) map[string]string {
	t.Helper()
	got, err := r.Solve(context.Background(), root)
	if err != nil {
		t.Fatalf("solve %v: %v", root, err)
	}
	return got
}

func snapshotOf(t *testing.T, r *Repository, root map[string]string) (*Snapshot, map[string]Range, []string) {
	t.Helper()
	r.mu.RLock()
	snap := r.snapshotLocked()
	r.mu.RUnlock()
	parsed := map[string]Range{}
	names := make([]string, 0, len(root))
	for name, text := range root {
		rng, err := ParseRange(text)
		if err != nil {
			t.Fatalf("root range %s: %v", text, err)
		}
		parsed[name] = rng
		names = append(names, name)
	}
	sort.Strings(names)
	return snap, parsed, names
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestBasicHighestVersion(t *testing.T) {
	r := NewRepository()
	mustPublish(t, r, "a", "1.0.0", nil)
	mustPublish(t, r, "a", "1.2.0", nil)
	mustPublish(t, r, "a", "1.1.0", nil)
	got := solveParsed(t, r, map[string]string{"a": ">=1.0.0"})
	if got["a"] != "1.2.0" {
		t.Fatalf("want 1.2.0, got %v", got)
	}
}

func TestBacktrackingRequired(t *testing.T) {
	r := NewRepository()
	mustPublish(t, r, "b", "1.0.0", nil)
	mustPublish(t, r, "a", "2.0.0", map[string]string{"b": "~2.0.0"})
	mustPublish(t, r, "a", "1.0.0", map[string]string{"b": "~1.0.0"})
	root := map[string]string{"a": "^1.0.0"}
	got := solveParsed(t, r, root)
	if got["a"] != "1.0.0" || got["b"] != "1.0.0" {
		t.Fatalf("expected backtrack to a@1.0.0, got %v", got)
	}
	snap, parsed, order := snapshotOf(t, r, root)
	want, err := bruteForceSolve(snap, parsed, order)
	if err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(got, want) {
		t.Fatalf("dfs=%v brute=%v", got, want)
	}
}

func TestYankExcludesHighest(t *testing.T) {
	r := NewRepository()
	mustPublish(t, r, "a", "1.0.0", nil)
	mustPublish(t, r, "a", "2.0.0", nil)
	if err := r.Yank("a", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	got := solveParsed(t, r, map[string]string{"a": ">=1.0.0"})
	if got["a"] != "1.0.0" {
		t.Fatalf("yanked 2.0.0 must not be selected, got %v", got)
	}
	if err := r.Publish(PackageVersion{Name: "a", Version: "2.0.0"}); !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("yanked version still exists; want ErrDuplicateVersion, got %v", err)
	}
}

func TestCircularDependencies(t *testing.T) {
	r := NewRepository()
	mustPublish(t, r, "a", "1.0.0", map[string]string{"b": "^1.0.0"})
	mustPublish(t, r, "b", "1.0.0", map[string]string{"a": ">=1.0.0"})
	got := solveParsed(t, r, map[string]string{"a": ">=1.0.0"})
	if got["a"] != "1.0.0" || got["b"] != "1.0.0" {
		t.Fatalf("circular deps unsolved: %v", got)
	}
}

func TestSelfDependency(t *testing.T) {
	r := NewRepository()
	mustPublish(t, r, "a", "1.0.0", map[string]string{"a": "^1.0.0"})
	got := solveParsed(t, r, map[string]string{"a": "^1.0.0"})
	if got["a"] != "1.0.0" || len(got) != 1 {
		t.Fatalf("self dependency must resolve to single selection, got %v", got)
	}
	mustPublish(t, r, "a", "2.0.0", map[string]string{"a": "~1.0.0"})
	got = solveParsed(t, r, map[string]string{"a": ">=1.0.0"})
	if got["a"] != "1.0.0" {
		t.Fatalf("a@2.0.0 self-constrained to ~1.0.0; want 1.0.0, got %v", got)
	}
}

func TestByteOrderSearchProducesDifferentResult(t *testing.T) {
	r := NewRepository()
	mustPublish(t, r, "c", "1.0.0", nil)
	mustPublish(t, r, "c", "2.0.0", nil)
	mustPublish(t, r, "a", "1.0.0", map[string]string{"c": "~2.0.0"})
	mustPublish(t, r, "a", "2.0.0", map[string]string{"c": "~1.0.0"})
	mustPublish(t, r, "b", "1.0.0", map[string]string{"c": "~1.0.0"})
	mustPublish(t, r, "b", "2.0.0", map[string]string{"c": "~2.0.0"})

	root := map[string]string{"a": ">=1.0.0 <3.0.0", "b": ">=1.0.0 <3.0.0"}
	got := solveParsed(t, r, root)
	if got["a"] != "2.0.0" || got["b"] != "1.0.0" || got["c"] != "1.0.0" {
		t.Fatalf("fixed a-first order wants a=2 b=1 c=1, got %v", got)
	}
	alt := solveWithOrder(t, r, root, "b")
	if alt["a"] != "1.0.0" || alt["b"] != "2.0.0" || alt["c"] != "2.0.0" {
		t.Fatalf("b-first wants a=1 b=2 c=2, got %v", alt)
	}
	if mapsEqual(got, alt) {
		t.Fatal("two search orders must produce different solutions")
	}

	snap, parsed, order := snapshotOf(t, r, root)
	want, err := bruteForceSolve(snap, parsed, order)
	if err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(got, want) {
		t.Fatalf("dfs=%v brute=%v", got, want)
	}
}

func solveWithOrder(t *testing.T, r *Repository, root map[string]string, first string) map[string]string {
	t.Helper()
	snap, parsed, _ := snapshotOf(t, r, root)
	constraints := map[string][]Range{}
	constrained := map[string]bool{}
	rest := ""
	for name := range parsed {
		if name != first {
			rest = name
		}
	}
	for _, name := range []string{first, rest} {
		constraints[name] = []Range{parsed[name]}
		constrained[name] = true
	}
	chosen := map[string]Version{}
	var dfs func() bool
	dfs = func() bool {
		name := pickFirst(constrained, chosen, first)
		if name == "" {
			return true
		}
		for _, v := range snap.candidateVersions(name) {
			if !allRangesAccept(v, constraints[name]) {
				continue
			}
			deps, depNames := snap.dependencies(name, v)
			chosen[name] = v
			for _, dep := range depNames {
				constraints[dep] = append(constraints[dep], deps[dep])
				constrained[dep] = true
			}
			if dfs() {
				return true
			}
			for _, dep := range depNames {
				constraints[dep] = constraints[dep][:len(constraints[dep])-1]
			}
			delete(chosen, name)
		}
		return false
	}
	if !dfs() {
		t.Fatal("alt-order no solution")
	}
	out := map[string]string{}
	for name, v := range chosen {
		out[name] = v.raw
	}
	return out
}

func pickFirst(constrained map[string]bool, chosen map[string]Version, first string) string {
	if constrained[first] {
		if _, done := chosen[first]; !done {
			return first
		}
	}
	return smallestConstrainedUndecided(constrained, chosen)
}

func TestPrereleaseSelection(t *testing.T) {
	r := NewRepository()
	mustPublish(t, r, "a", "1.0.0", nil)
	mustPublish(t, r, "a", "1.0.0-alpha.1", nil)
	mustPublish(t, r, "a", "1.0.0-alpha.beta", nil)
	got := solveParsed(t, r, map[string]string{"a": ">=1.0.0-alpha <2.0.0"})
	if got["a"] != "1.0.0" {
		t.Fatalf("stable 1.0.0 must win, got %v", got)
	}
	if err := r.Yank("a", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	got = solveParsed(t, r, map[string]string{"a": ">=1.0.0-alpha <2.0.0"})
	if got["a"] != "1.0.0-alpha.beta" {
		t.Fatalf("want 1.0.0-alpha.beta, got %v", got)
	}
	if _, err := r.Solve(context.Background(), map[string]string{"a": ">=1.0.0 <2.0.0"}); !errors.Is(err, ErrNoSolution) {
		t.Fatalf("want ErrNoSolution, got %v", err)
	}
}

func TestErrorCauses(t *testing.T) {
	r := NewRepository()
	if err := r.Publish(PackageVersion{Name: "a", Version: "bad"}); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("want ErrInvalidVersion, got %v", err)
	}
	mustPublish(t, r, "a", "1.0.0", nil)
	if err := r.Publish(PackageVersion{Name: "a", Version: "1.0.0"}); !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("want ErrDuplicateVersion, got %v", err)
	}
	if err := r.Publish(PackageVersion{Name: "a", Version: "1.1.0", Dependencies: map[string]string{"b": "bad"}}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("want ErrInvalidRange, got %v", err)
	}
	if err := r.Yank("a", "9.9.9"); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
	if err := r.Yank("ghost", "1.0.0"); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("want ErrVersionNotFound, got %v", err)
	}
	if err := r.Yank("a", "nope"); !errors.Is(err, ErrInvalidVersion) {
		t.Fatalf("want ErrInvalidVersion, got %v", err)
	}
	if _, err := r.Solve(context.Background(), map[string]string{"a": "bad"}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("want ErrInvalidRange, got %v", err)
	}

	mustPublish(t, r, "d", "1.0.0", map[string]string{"ghost": "^1.0.0"})
	if _, err := r.Solve(context.Background(), map[string]string{"d": "^1.0.0"}); !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("want ErrPackageNotFound, got %v", err)
	}

	mustPublish(t, r, "e", "1.0.0", map[string]string{"a": "~2.0.0"})
	if _, err := r.Solve(context.Background(), map[string]string{"e": "^1.0.0"}); !errors.Is(err, ErrNoSolution) {
		t.Fatalf("want ErrNoSolution, got %v", err)
	}

	if err := r.Publish(PackageVersion{Name: "new", Version: "1.0.0", Dependencies: map[string]string{"x": "bad"}}); err == nil {
		t.Fatal("expected publish rejection")
	}
	if err := r.Publish(PackageVersion{Name: "new", Version: "1.0.0"}); err != nil {
		t.Fatalf("rejected publish must not mutate repo: %v", err)
	}
}

func TestDeterminismAndOrderIndependence(t *testing.T) {
	pvs := []PackageVersion{
		{Name: "a", Version: "1.0.0", Dependencies: map[string]string{"b": "~1.0.0"}},
		{Name: "a", Version: "1.1.0", Dependencies: map[string]string{"b": "~1.1.0"}},
		{Name: "b", Version: "1.0.0"},
		{Name: "b", Version: "1.1.0"},
	}
	r1, r2 := NewRepository(), NewRepository()
	for _, pv := range pvs {
		mustPublish(t, r1, pv.Name, pv.Version, pv.Dependencies)
	}
	for i := len(pvs) - 1; i >= 0; i-- {
		mustPublish(t, r2, pvs[i].Name, pvs[i].Version, pvs[i].Dependencies)
	}
	root := map[string]string{"a": ">=1.0.0"}
	g1 := solveParsed(t, r1, root)
	g2 := solveParsed(t, r2, root)
	g3 := solveParsed(t, r1, root)
	if !mapsEqual(g1, g2) || !mapsEqual(g1, g3) {
		t.Fatalf("solutions must be identical: %v %v %v", g1, g2, g3)
	}
	// 根要求 ^1.0.0：a=1.1.0 要求 b~1.1.0，本可成立；这里改为根要求 ~1.0.0 迫使回溯。
	if g1["a"] != "1.1.0" || g1["b"] != "1.1.0" {
		t.Fatalf("unexpected solution %v", g1)
	}
	snap, parsed, order := snapshotOf(t, r1, root)
	want, err := bruteForceSolve(snap, parsed, order)
	if err != nil || !mapsEqual(g1, want) {
		t.Fatalf("brute mismatch: %v %v", want, err)
	}
}

// TestBruteForceParity 在组合丰富的小仓库上对照 DFS 与朴素枚举。
func TestBruteForceParity(t *testing.T) {
	packages := []string{"a", "b", "c"}
	versions := []string{"1.0.0", "1.1.0", "2.0.0"}
	// 手工构建一个组合丰富的小仓库。
	r := NewRepository()
	for _, name := range packages {
		for _, v := range versions {
			deps := map[string]string{}
			if name != "c" {
				deps["c"] = ">=1.0.0"
			}
			if v == "2.0.0" && name == "a" {
				deps["b"] = "~1.0.0"
			} else {
				deps["b"] = ">=1.0.0"
			}
			if name == "c" && v == "2.0.0" {
				deps["a"] = "<2.0.0"
			}
			mustPublish(t, r, name, v, deps)
		}
	}
	roots := []map[string]string{
		{"a": "^1.0.0"},
		{"a": ">=1.0.0"},
		{"a": "^1.0.0", "b": "~1.0.0"},
		{"a": ">=1.0.0 <3.0.0", "c": "^1.0.0"},
	}
	for _, root := range roots {
		got, gerr := r.Solve(context.Background(), root)
		snap, parsed, order := snapshotOf(t, r, root)
		want, werr := bruteForceSolve(snap, parsed, order)
		if !errors.Is(gerr, werr) {
			t.Fatalf("root=%v err mismatch: %v vs %v", root, gerr, werr)
		}
		if gerr == nil && !mapsEqual(got, want) {
			t.Fatalf("root=%v dfs=%v brute=%v", root, got, want)
		}
	}
}

func TestConcurrentPublishYankSolve(t *testing.T) {
	r := NewRepository()
	for _, name := range []string{"a", "b", "c"} {
		mustPublish(t, r, name, "1.0.0", map[string]string{"a": ">=1.0.0"})
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		v := fmt.Sprintf("1.0.%d", i+1)
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = r.Publish(PackageVersion{Name: "a", Version: v})
		}()
		go func() {
			defer wg.Done()
			_ = r.Yank("a", "1.0.0")
			_ = r.Yank("a", "nope-x")
		}()
		go func() {
			defer wg.Done()
			sol, err := r.Solve(context.Background(), map[string]string{"a": ">=1.0.0", "b": "^1.0.0"})
			if err == nil {
				if sol["a"] == "" || sol["b"] != "1.0.0" || len(sol) != 2 {
					t.Errorf("snapshot inconsistency: %v", sol)
				}
			}
		}()
	}
	wg.Wait()
}

func TestLoggerOutput(t *testing.T) {
	r := NewRepository()
	mustPublish(t, r, "a", "2.0.0", map[string]string{"b": "~2.0.0"})
	mustPublish(t, r, "a", "1.0.0", map[string]string{"b": "^1.0.0"})
	mustPublish(t, r, "b", "1.0.0", nil)
	l := &bufLogger{}
	sol, err := r.Solve(WithLogger(context.Background(), l), map[string]string{"a": ">=1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	out := l.buf.String()
	if sol["a"] != "1.0.0" {
		t.Fatalf("want 1.0.0, got %v", sol)
	}
	for _, want := range []string{"solve: root=", "decide:", "backtrack: a@2.0.0", "try: a@1.0.0", "solve: solution="} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("log missing %q\n%s", want, out)
		}
	}
	t.Log("\n" + out)
}
