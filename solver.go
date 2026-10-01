package resolver

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

type solveConfig struct {
	logger io.Writer
}

type SolveOption func(*solveConfig)

type solver struct {
	snapshot    repositorySnapshot
	constraints map[string][]versionRange
	selected    map[string]*packageVersion
	candidates  map[string][]*packageVersion
	logger      io.Writer
}

type searchFailure struct {
	missingPackage string
	noSolution     bool
}

func WithLogger(logger io.Writer) SolveOption {
	return func(config *solveConfig) {
		config.logger = logger
	}
}

func (r *Repository) Solve(root map[string]string, options ...SolveOption) (map[string]string, error) {
	config := solveConfig{}
	for _, option := range options {
		option(&config)
	}

	constraints := make(map[string][]versionRange, len(root))
	for packageName, rangeText := range root {
		if packageName == "" {
			return nil, ErrInvalidPackageName
		}
		parsedRange, err := parseRange(rangeText)
		if err != nil {
			return nil, err
		}
		constraints[packageName] = append(constraints[packageName], parsedRange)
	}

	snapshot := r.currentSnapshot()
	for packageName := range constraints {
		if _, exists := snapshot.packages[packageName]; !exists {
			return nil, ErrPackageNotFound
		}
	}

	state := &solver{
		snapshot:    snapshot,
		constraints: constraints,
		selected:    map[string]*packageVersion{},
		candidates:  map[string][]*packageVersion{},
		logger:      config.logger,
	}

	logf(config.logger, "input root=%s", formatRoot(root))
	logf(config.logger, "snapshot packages=%s", formatPackageList(snapshot.packages))
	failure := state.search()
	if failure != nil {
		if failure.missingPackage != "" {
			logf(config.logger, "output error=package-not-found package=%q", failure.missingPackage)
			return nil, ErrPackageNotFound
		}
		logf(config.logger, "output error=no-solution")
		return nil, ErrNoSolution
	}

	result := make(map[string]string, len(state.selected))
	for packageName, version := range state.selected {
		result[packageName] = version.version
	}
	logf(config.logger, "output solution=%s", formatSolution(result))
	return result, nil
}

func (state *solver) search() *searchFailure {
	if len(state.selected) == len(state.constraints) {
		logf(state.logger, "decision complete solution=%s", formatSelected(state.selected))
		return nil
	}

	packageName := state.smallestConstrainedUnselected()
	candidates := state.eligibleCandidates(packageName)
	logf(state.logger, "decision package=%q candidates=%s", packageName, formatCandidates(candidates))
	if len(candidates) == 0 {
		logf(state.logger, "reject package=%q reason=no-eligible-candidate", packageName)
		return &searchFailure{noSolution: true}
	}

	sawMissingDependency := false
	missingPackageName := ""
	for _, candidate := range candidates {
		if missingDependency := state.missingDependency(candidate); missingDependency != "" {
			logf(state.logger, "reject package=%q version=%q reason=missing-package dependency=%q",
				packageName, candidate.version, missingDependency)
			sawMissingDependency = true
			missingPackageName = missingDependency
			continue
		}

		rollback := state.choose(packageName, candidate)
		logf(state.logger, "try package=%q version=%q constraints=%s selected=%s",
			packageName, candidate.version, formatConstraints(state.constraints), formatSelected(state.selected))

		if state.conflictsWithSelected() {
			logf(state.logger, "reject package=%q version=%q reason=existing-selection-conflict",
				packageName, candidate.version)
			rollback()
			continue
		}

		if failure := state.search(); failure == nil {
			return nil
		} else if failure.missingPackage != "" {
			rollback()
			sawMissingDependency = true
			missingPackageName = failure.missingPackage
			continue
		}

		logf(state.logger, "backtrack package=%q version=%q", packageName, candidate.version)
		rollback()
	}

	if sawMissingDependency {
		return &searchFailure{missingPackage: missingPackageName}
	}
	return &searchFailure{noSolution: true}
}

func (state *solver) choose(packageName string, candidate *packageVersion) func() {
	state.selected[packageName] = candidate
	addedConstraints := make(map[string]int)

	for dependencyName, dependencyRange := range candidate.ranges {
		state.constraints[dependencyName] = append(state.constraints[dependencyName], dependencyRange)
		addedConstraints[dependencyName]++
	}

	return func() {
		delete(state.selected, packageName)
		state.rollbackConstraints(addedConstraints)
	}
}

func (state *solver) rollbackConstraints(addedConstraints map[string]int) {
	for packageName, count := range addedConstraints {
		state.constraints[packageName] = state.constraints[packageName][:len(state.constraints[packageName])-count]
		if len(state.constraints[packageName]) == 0 {
			delete(state.constraints, packageName)
		}
	}
}

func (state *solver) missingDependency(candidate *packageVersion) string {
	for dependencyName := range candidate.ranges {
		if _, exists := state.snapshot.packages[dependencyName]; !exists {
			return dependencyName
		}
	}
	return ""
}

func (state *solver) smallestConstrainedUnselected() string {
	names := make([]string, 0, len(state.constraints)-len(state.selected))
	for packageName := range state.constraints {
		if _, selected := state.selected[packageName]; !selected {
			names = append(names, packageName)
		}
	}
	sort.Strings(names)
	return names[0]
}

func (state *solver) eligibleCandidates(packageName string) []*packageVersion {
	candidates := []*packageVersion{}
	for _, candidate := range state.availableCandidates(packageName) {
		if !state.satisfiesAll(candidate, packageName) {
			continue
		}
		if selfRange, hasSelfDependency := candidate.ranges[packageName]; hasSelfDependency && !selfRange.allows(candidate.parsed) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func (state *solver) availableCandidates(packageName string) []*packageVersion {
	if candidates, cached := state.candidates[packageName]; cached {
		return candidates
	}
	candidates := []*packageVersion{}
	for _, candidate := range state.snapshot.packages[packageName] {
		if !candidate.yanked {
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return compareVersion(candidates[left].parsed, candidates[right].parsed) > 0
	})
	state.candidates[packageName] = candidates
	return candidates
}

func (state *solver) satisfiesAll(candidate *packageVersion, packageName string) bool {
	for _, constraint := range state.constraints[packageName] {
		if !constraint.allows(candidate.parsed) {
			return false
		}
	}
	return true
}

func (state *solver) conflictsWithSelected() bool {
	for packageName, candidate := range state.selected {
		if !state.satisfiesAll(candidate, packageName) {
			return true
		}
	}
	return false
}

func logf(logger io.Writer, format string, arguments ...any) {
	if logger != nil {
		fmt.Fprintf(logger, format+"\n", arguments...)
	}
}

func formatRoot(root map[string]string) string {
	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", key, root[key]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatPackageList(packages map[string]map[string]*packageVersion) string {
	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}
	sort.Strings(names)
	return "[" + strings.Join(names, ", ") + "]"
}

func formatSolution(solution map[string]string) string {
	names := make([]string, 0, len(solution))
	for name := range solution {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+solution[name])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatSelected(selected map[string]*packageVersion) string {
	solution := make(map[string]string, len(selected))
	for name, version := range selected {
		solution[name] = version.version
	}
	return formatSolution(solution)
}

func formatCandidates(candidates []*packageVersion) string {
	parts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parts = append(parts, candidate.version)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatConstraints(constraints map[string][]versionRange) string {
	names := make([]string, 0, len(constraints))
	for name := range constraints {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+":"+fmt.Sprint(len(constraints[name])))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
