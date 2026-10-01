package resolver

import (
	"sort"
	"testing"
)

func buildSearchOrderRepository(t *testing.T) *Repository {
	t.Helper()
	repo := NewRepository()
	publishOrFatal(t, repo, "a", "1.0.0", map[string]string{"shared": "^2.0.0"})
	publishOrFatal(t, repo, "a", "2.0.0", map[string]string{"shared": "^1.0.0"})
	publishOrFatal(t, repo, "b", "1.0.0", map[string]string{"shared": "^1.0.0"})
	publishOrFatal(t, repo, "b", "2.0.0", map[string]string{"shared": "^2.0.0"})
	publishOrFatal(t, repo, "shared", "1.0.0", nil)
	publishOrFatal(t, repo, "shared", "2.0.0", nil)
	return repo
}

func publishOrFatal(t *testing.T, repo *Repository, name, version string, dependencies map[string]string) {
	t.Helper()
	if err := repo.Publish(name, version, dependencies); err != nil {
		t.Fatalf("publish %s@%s: %v", name, version, err)
	}
}

func parseVersionOrFatal(t *testing.T, text string) semanticVersion {
	t.Helper()
	version, err := parseVersion(text)
	if err != nil {
		t.Fatalf("parse version %s: %v", text, err)
	}
	return version
}

func parseRangeOrFatal(t *testing.T, text string) versionRange {
	t.Helper()
	constraint, err := parseRange(text)
	if err != nil {
		t.Fatalf("parse range %s: %v", text, err)
	}
	return constraint
}

func assertSolve(t *testing.T, repo *Repository, root map[string]string, want map[string]string) {
	t.Helper()
	got, err := repo.Solve(root)
	if err != nil {
		t.Fatal(err)
	}
	if !mapsEqual(got, want) {
		t.Fatalf("solve() = %v, want %v", got, want)
	}
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func assertBruteForceMatches(t *testing.T, repo *Repository, root map[string]string) {
	t.Helper()
	solution, err := repo.Solve(root)
	if err != nil {
		t.Fatal(err)
	}
	oracle := bruteForceFirst(t, repo.currentSnapshot(), root)
	if !mapsEqual(solution, oracle) {
		t.Fatalf("solver %v differs from brute force %v", solution, oracle)
	}
}

func bruteForceFirst(t *testing.T, snapshot repositorySnapshot, root map[string]string) map[string]string {
	t.Helper()
	constraints := map[string][]versionRange{}
	for name, text := range root {
		constraints[name] = append(constraints[name], parseRangeOrFatal(t, text))
	}

	packageNames := reachablePackageNames(t, snapshot, root)
	solutions := enumerateSolutions(t, snapshot, packageNames, 0, map[string]string{}, constraints)
	if len(solutions) == 0 {
		t.Fatal("brute force found no solutions")
	}
	sort.Slice(solutions, func(i, j int) bool {
		return compareSolution(solutions[i], solutions[j], snapshot) < 0
	})
	return solutions[0]
}

func reachablePackageNames(t *testing.T, snapshot repositorySnapshot, root map[string]string) []string {
	t.Helper()
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		versions := snapshot.packages[name]
		if versions == nil {
			t.Fatalf("brute force missing package %q", name)
		}
		for _, version := range versions {
			for dependency := range version.dependencies {
				visit(dependency)
			}
		}
	}
	for name := range root {
		visit(name)
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func enumerateSolutions(
	t *testing.T,
	snapshot repositorySnapshot,
	packageNames []string,
	index int,
	selected map[string]string,
	constraints map[string][]versionRange,
) []map[string]string {
	t.Helper()
	if index == len(packageNames) {
		return []map[string]string{cloneStringMap(selected)}
	}

	name := packageNames[index]
	versions := []*packageVersion{}
	for _, candidate := range snapshot.packages[name] {
		versions = append(versions, candidate)
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareVersion(versions[i].parsed, versions[j].parsed) > 0
	})

	var solutions []map[string]string
	for _, candidate := range versions {
		if candidate.yanked {
			continue
		}
		nextConstraints := appendConstraints(t, constraints, name, candidate)
		if !rangesAllow(nextConstraints[name], candidate.parsed) {
			continue
		}
		selected[name] = candidate.version
		solutions = append(solutions, enumerateSolutions(t, snapshot, packageNames, index+1, selected, nextConstraints)...)
		delete(selected, name)
	}
	return solutions
}

func appendConstraints(t *testing.T, existing map[string][]versionRange, name string, candidate *packageVersion) map[string][]versionRange {
	t.Helper()
	next := map[string][]versionRange{}
	for packageName, ranges := range existing {
		next[packageName] = append([]versionRange(nil), ranges...)
	}
	for dependencyName, text := range candidate.dependencies {
		next[dependencyName] = append(next[dependencyName], parseRangeOrFatal(t, text))
	}
	if !rangesAllow(next[name], candidate.parsed) {
		return next
	}
	return next
}

func rangesAllow(ranges []versionRange, version semanticVersion) bool {
	for _, constraint := range ranges {
		if !constraint.allows(version) {
			return false
		}
	}
	return true
}

func compareSolution(left, right map[string]string, snapshot repositorySnapshot) int {
	names := make([]string, 0)
	seen := map[string]bool{}
	for name := range left {
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	for name := range right {
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if result := compareVersion(snapshot.packages[name][left[name]].parsed,
			snapshot.packages[name][right[name]].parsed); result != 0 {
			return -result
		}
	}
	return 0
}

func solveInPackageOrder(
	t *testing.T,
	repo *Repository,
	root map[string]string,
	order []string,
) map[string]string {
	t.Helper()
	snapshot := repo.currentSnapshot()
	constraints := map[string][]versionRange{}
	for name, text := range root {
		constraints[name] = append(constraints[name], parseRangeOrFatal(t, text))
	}
	selected := map[string]string{}
	if !searchWithOrder(t, snapshot, constraints, selected, order) {
		t.Fatal("alternative-order search found no solution")
	}
	return selected
}

func searchWithOrder(
	t *testing.T,
	snapshot repositorySnapshot,
	constraints map[string][]versionRange,
	selected map[string]string,
	order []string,
) bool {
	t.Helper()
	if len(selected) == len(constraints) {
		return true
	}

	name := firstOrderedUnselected(constraints, selected, order)
	versions := []*packageVersion{}
	for _, candidate := range snapshot.packages[name] {
		versions = append(versions, candidate)
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareVersion(versions[i].parsed, versions[j].parsed) > 0
	})
	for _, candidate := range versions {
		if candidate.yanked || !rangesAllow(constraints[name], candidate.parsed) {
			continue
		}
		nextConstraints := appendConstraints(t, constraints, name, candidate)
		if !rangesAllow(nextConstraints[name], candidate.parsed) {
			continue
		}
		selected[name] = candidate.version
		if searchWithOrder(t, snapshot, nextConstraints, selected, order) {
			return true
		}
		delete(selected, name)
	}
	return false
}

func firstOrderedUnselected(constraints map[string][]versionRange, selected map[string]string, order []string) string {
	for _, name := range order {
		if _, constrained := constraints[name]; constrained {
			if _, done := selected[name]; !done {
				return name
			}
		}
	}
	panic("incomplete custom search order")
}
