package resolver

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestCaretUpperBounds(t *testing.T) {
	cases := []struct {
		constraint string
		allowed    string
		denied     string
	}{
		{"^0.0.3", "0.0.3", "0.0.4"},
		{"^0.2.1", "0.2.9", "0.3.0"},
		{"^1.2.3", "1.9.9", "2.0.0"},
	}

	for _, tc := range cases {
		t.Run(tc.constraint, func(t *testing.T) {
			constraint := parseRangeOrFatal(t, tc.constraint)
			if !constraint.allows(parseVersionOrFatal(t, tc.allowed)) {
				t.Fatalf("%s did not allow %s", tc.constraint, tc.allowed)
			}
			if constraint.allows(parseVersionOrFatal(t, tc.denied)) {
				t.Fatalf("%s unexpectedly allowed %s", tc.constraint, tc.denied)
			}
		})
	}
}

func TestPrereleaseOrderingAndGate(t *testing.T) {
	texts := []string{"1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0"}
	versions := make([]semanticVersion, len(texts))
	for index, text := range texts {
		versions[index] = parseVersionOrFatal(t, text)
	}
	if compareVersion(versions[0], versions[1]) >= 0 || compareVersion(versions[1], versions[2]) >= 0 {
		t.Fatalf("bad prerelease order: %s", strings.Join(texts, ", "))
	}

	gated := parseRangeOrFatal(t, ">=1.0.0-alpha.1 <1.0.0")
	if !gated.allows(versions[0]) || !gated.allows(versions[1]) || gated.allows(versions[2]) {
		t.Fatal("same-triple prerelease comparator did not gate candidates correctly")
	}

	other := parseRangeOrFatal(t, ">=0.9.0 <1.0.0")
	if other.allows(versions[0]) {
		t.Fatal("prerelease was allowed without same-triple prerelease comparator")
	}
}

func TestInvalidVersionsAndRanges(t *testing.T) {
	invalidVersions := []string{
		"", "1.0", "1.0.0.0", "01.0.0", "1.0.99999", "1.0.0-",
		"1.0.0-01", "1.0.0-alpha..1", "1.0.0-alpha_1", "v1.0.0",
	}
	for _, text := range invalidVersions {
		if _, err := parseVersion(text); !errors.Is(err, ErrInvalidVersion) {
			t.Fatalf("parseVersion(%q) error = %v, want %v", text, err, ErrInvalidVersion)
		}
	}

	invalidRanges := []string{"", "   ", "^ 1.0.0", "1.0.0", ">", "=>1.0.0"}
	for _, text := range invalidRanges {
		if _, err := parseRange(text); !errors.Is(err, ErrInvalidRange) {
			t.Fatalf("parseRange(%q) error = %v, want %v", text, err, ErrInvalidRange)
		}
	}

	repo := NewRepository()
	if _, err := repo.Solve(map[string]string{"app": ">= 1.0.0"}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("root range error = %v, want %v", err, ErrInvalidRange)
	}
}

func TestBacktrackingSearchOrderYankAndCycle(t *testing.T) {
	repo := buildSearchOrderRepository(t)
	want := map[string]string{"a": "2.0.0", "b": "1.0.0", "shared": "1.0.0"}
	assertSolve(t, repo, map[string]string{"a": ">=1.0.0", "b": ">=1.0.0"}, want)
	assertBruteForceMatches(t, repo, map[string]string{"a": ">=1.0.0", "b": ">=1.0.0"})

	alternative := solveInPackageOrder(t, repo,
		map[string]string{"a": ">=1.0.0", "b": ">=1.0.0"}, []string{"b", "a", "shared"})
	if alternative["a"] != "1.0.0" || alternative["b"] != "2.0.0" || alternative["shared"] != "2.0.0" {
		t.Fatalf("alternative b-first order returned %v", alternative)
	}

	if err := repo.Yank("shared", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Yank("shared", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Solve(map[string]string{"a": ">=1.0.0"}); !errors.Is(err, ErrNoSolution) {
		t.Fatalf("yanked-only solve error = %v, want %v", err, ErrNoSolution)
	}

	cyclic := NewRepository()
	publishOrFatal(t, cyclic, "left", "1.0.0", map[string]string{"right": "^1.0.0"})
	publishOrFatal(t, cyclic, "right", "1.0.0", map[string]string{"left": "^1.0.0"})
	assertSolve(t, cyclic, map[string]string{"left": "^1.0.0"},
		map[string]string{"left": "1.0.0", "right": "1.0.0"})
}

func TestYankSelectsNextHighest(t *testing.T) {
	repo := NewRepository()
	publishOrFatal(t, repo, "app", "2.0.0", nil)
	publishOrFatal(t, repo, "app", "1.5.0", nil)
	if err := repo.Yank("app", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	assertSolve(t, repo, map[string]string{"app": ">=1.0.0"}, map[string]string{"app": "1.5.0"})
	if !repo.packages["app"]["2.0.0"].yanked {
		t.Fatal("yanked version must remain present")
	}
}

func TestMissingPackageAndNoSolutionAreDistinct(t *testing.T) {
	repo := NewRepository()
	publishOrFatal(t, repo, "choice", "2.0.0", map[string]string{"ghost": "^1.0.0"})
	publishOrFatal(t, repo, "choice", "1.0.0", nil)

	_, err := repo.Solve(map[string]string{"choice": "^2.0.0"})
	if !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("error = %v, want %v", err, ErrPackageNotFound)
	}

	_, err = repo.Solve(map[string]string{"choice": ">=3.0.0"})
	if !errors.Is(err, ErrNoSolution) {
		t.Fatalf("error = %v, want %v", err, ErrNoSolution)
	}

	assertSolve(t, repo, map[string]string{"choice": ">=1.0.0"}, map[string]string{"choice": "1.0.0"})
	_, err = repo.Solve(map[string]string{"absent": "^1.0.0"})
	if !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("root missing error = %v", err)
	}
}

func TestRejectedOperationsDoNotMutateRepository(t *testing.T) {
	repo := NewRepository()
	publishOrFatal(t, repo, "app", "1.0.0", nil)

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"invalid version", func() error { return repo.Publish("app", "1.0", nil) }, ErrInvalidVersion},
		{"duplicate", func() error { return repo.Publish("app", "1.0.0", nil) }, ErrDuplicateVersion},
		{"invalid range", func() error {
			return repo.Publish("app", "2.0.0", map[string]string{"x": "^ 1.0.0"})
		}, ErrInvalidRange},
		{"yank missing", func() error { return repo.Yank("app", "9.9.9") }, ErrVersionNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.run(), tc.want) {
				t.Fatalf("want %v", tc.want)
			}
		})
	}
	if len(repo.packages["app"]) != 1 {
		t.Fatal("rejected operation mutated repository")
	}
}

func TestDeterminismAndConcurrency(t *testing.T) {
	first := buildSearchOrderRepository(t)
	second := NewRepository()
	publishOrFatal(t, second, "shared", "2.0.0", nil)
	publishOrFatal(t, second, "a", "1.0.0", map[string]string{"shared": "^2.0.0"})
	publishOrFatal(t, second, "b", "1.0.0", map[string]string{"shared": "^1.0.0"})
	publishOrFatal(t, second, "b", "2.0.0", map[string]string{"shared": "^2.0.0"})
	publishOrFatal(t, second, "shared", "1.0.0", nil)
	publishOrFatal(t, second, "a", "2.0.0", map[string]string{"shared": "^1.0.0"})

	root := map[string]string{"a": ">=1.0.0", "b": ">=1.0.0"}
	one, err := first.Solve(root)
	if err != nil {
		t.Fatal(err)
	}
	two, err := second.Solve(root)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(one) != fmt.Sprint(two) {
		t.Fatalf("publication order changed result: %v vs %v", one, two)
	}
	again, err := first.Solve(root)
	if err != nil || fmt.Sprint(again) != fmt.Sprint(one) {
		t.Fatalf("repeated solve differs: %v vs %v, err=%v", again, one, err)
	}

	repo := NewRepository()
	publishOrFatal(t, repo, "base", "0.0.1", nil)
	var wait sync.WaitGroup
	for number := 2; number <= 30; number++ {
		wait.Add(2)
		go func(n int) {
			defer wait.Done()
			_ = repo.Publish("base", fmt.Sprintf("0.0.%d", n), nil)
		}(number)
		go func(n int) {
			defer wait.Done()
			_, _ = repo.Solve(map[string]string{"base": ">=0.0.1"})
		}(number)
	}
	wait.Wait()
	if err := repo.Yank("base", "0.0.30"); err != nil {
		t.Fatal(err)
	}
	assertSolve(t, repo, map[string]string{"base": ">=0.0.1"}, map[string]string{"base": "0.0.29"})
}

func TestLoggingShowsInputOutputAndReasoning(t *testing.T) {
	repo := buildSearchOrderRepository(t)
	var log bytes.Buffer
	result, err := repo.Solve(map[string]string{"a": ">=1.0.0", "b": ">=1.0.0"}, WithLogger(&log))
	if err != nil {
		t.Fatal(err)
	}
	text := log.String()
	t.Log(text)
	for _, wanted := range []string{"input root=", "decision package=", "try package=", "reject package=", "backtrack package=", "output solution="} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("log missing %q", wanted)
		}
	}
	if result["b"] != "1.0.0" {
		t.Fatalf("expected b 1.0.0, got %s", result["b"])
	}
}
